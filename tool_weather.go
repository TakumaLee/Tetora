package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Base URLs for Open-Meteo APIs (overridable in tests).
var (
	weatherBaseURL   = "https://api.open-meteo.com"
	geocodingBaseURL = "https://geocoding-api.open-meteo.com"
)

// weatherCodeDesc maps WMO weather codes to human-readable descriptions.
var weatherCodeDesc = map[int]string{
	0: "Clear sky", 1: "Mainly clear", 2: "Partly cloudy", 3: "Overcast",
	45: "Fog", 48: "Depositing rime fog",
	51: "Light drizzle", 53: "Moderate drizzle", 55: "Dense drizzle",
	61: "Slight rain", 63: "Moderate rain", 65: "Heavy rain",
	71: "Slight snow", 73: "Moderate snow", 75: "Heavy snow",
	77: "Snow grains", 80: "Slight rain showers", 81: "Moderate rain showers",
	82: "Violent rain showers", 85: "Slight snow showers", 86: "Heavy snow showers",
	95: "Thunderstorm", 96: "Thunderstorm with slight hail", 99: "Thunderstorm with heavy hail",
}

func toolWeatherCurrent(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Location string `json:"location"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	loc := args.Location
	if loc == "" {
		loc = cfg.Weather.Location
	}
	if loc == "" {
		return "", fmt.Errorf("location required (set in config or pass as parameter)")
	}

	lat, lon, name, err := geocodeLocation(loc)
	if err != nil {
		return "", err
	}

	apiURL := fmt.Sprintf("%s/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,weather_code,wind_speed_10m,apparent_temperature&timezone=auto",
		weatherBaseURL, lat, lon)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("weather API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("weather API returned %d", resp.StatusCode)
	}

	var result struct {
		Current struct {
			Temperature  float64 `json:"temperature_2m"`
			Humidity     float64 `json:"relative_humidity_2m"`
			WeatherCode  int     `json:"weather_code"`
			WindSpeed    float64 `json:"wind_speed_10m"`
			ApparentTemp float64 `json:"apparent_temperature"`
		} `json:"current"`
		CurrentUnits struct {
			Temperature string `json:"temperature_2m"`
			WindSpeed   string `json:"wind_speed_10m"`
		} `json:"current_units"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}

	desc := weatherCodeDesc[result.Current.WeatherCode]
	if desc == "" {
		desc = "Unknown"
	}

	return fmt.Sprintf("%s: %s\nTemperature: %.1f%s (feels like %.1f%s)\nHumidity: %.0f%%\nWind: %.1f %s",
		name, desc,
		result.Current.Temperature, result.CurrentUnits.Temperature,
		result.Current.ApparentTemp, result.CurrentUnits.Temperature,
		result.Current.Humidity,
		result.Current.WindSpeed, result.CurrentUnits.WindSpeed,
	), nil
}

func toolWeatherForecast(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Location string `json:"location"`
		Days     int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	loc := args.Location
	if loc == "" {
		loc = cfg.Weather.Location
	}
	if loc == "" {
		return "", fmt.Errorf("location required")
	}
	days := args.Days
	if days <= 0 || days > 7 {
		days = 3
	}

	lat, lon, name, err := geocodeLocation(loc)
	if err != nil {
		return "", err
	}

	apiURL := fmt.Sprintf("%s/v1/forecast?latitude=%.4f&longitude=%.4f&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max&timezone=auto&forecast_days=%d",
		weatherBaseURL, lat, lon, days)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("weather API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("weather API returned %d", resp.StatusCode)
	}

	var result struct {
		Daily struct {
			Time        []string  `json:"time"`
			WeatherCode []int     `json:"weather_code"`
			TempMax     []float64 `json:"temperature_2m_max"`
			TempMin     []float64 `json:"temperature_2m_min"`
			Precip      []float64 `json:"precipitation_sum"`
			PrecipProb  []float64 `json:"precipitation_probability_max"`
		} `json:"daily"`
		DailyUnits struct {
			TempMax string `json:"temperature_2m_max"`
		} `json:"daily_units"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d-day forecast:\n", name, days)
	for i := range result.Daily.Time {
		desc := weatherCodeDesc[result.Daily.WeatherCode[i]]
		if desc == "" {
			desc = "Unknown"
		}
		fmt.Fprintf(&sb, "\n%s: %s\n  High: %.1f%s / Low: %.1f%s\n  Precipitation: %.1fmm (%.0f%% chance)\n",
			result.Daily.Time[i], desc,
			result.Daily.TempMax[i], result.DailyUnits.TempMax,
			result.Daily.TempMin[i], result.DailyUnits.TempMax,
			result.Daily.Precip[i],
			result.Daily.PrecipProb[i],
		)
	}
	return sb.String(), nil
}

func geocodeLocation(location string) (lat, lon float64, name string, err error) {
	apiURL := geocodingBaseURL + "/v1/search?name=" + url.QueryEscape(location) + "&count=1"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return 0, 0, "", fmt.Errorf("geocoding error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, 0, "", fmt.Errorf("geocoding API returned %d", resp.StatusCode)
	}
	var result struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Name      string  `json:"name"`
			Country   string  `json:"country"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, "", fmt.Errorf("decode geocoding: %w", err)
	}
	if len(result.Results) == 0 {
		return 0, 0, "", fmt.Errorf("location %q not found", location)
	}
	r := result.Results[0]
	return r.Latitude, r.Longitude, fmt.Sprintf("%s, %s", r.Name, r.Country), nil
}
