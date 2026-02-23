package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolWeatherCurrent(t *testing.T) {
	// Mock geocoding server.
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"latitude": 35.6895, "longitude": 139.6917, "name": "Tokyo", "country": "Japan"},
			},
		})
	}))
	defer geoSrv.Close()

	// Mock weather server.
	wxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"current": map[string]any{
				"temperature_2m":       22.5,
				"relative_humidity_2m": 65.0,
				"weather_code":         1,
				"wind_speed_10m":       12.3,
				"apparent_temperature": 21.0,
			},
			"current_units": map[string]any{
				"temperature_2m": "°C",
				"wind_speed_10m": "km/h",
			},
		})
	}))
	defer wxSrv.Close()

	origGeo := geocodingBaseURL
	origWx := weatherBaseURL
	geocodingBaseURL = geoSrv.URL
	weatherBaseURL = wxSrv.URL
	defer func() {
		geocodingBaseURL = origGeo
		weatherBaseURL = origWx
	}()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"location": "Tokyo"})
	result, err := toolWeatherCurrent(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Tokyo, Japan") {
		t.Errorf("expected location in result, got: %s", result)
	}
	if !strings.Contains(result, "Mainly clear") {
		t.Errorf("expected weather description, got: %s", result)
	}
	if !strings.Contains(result, "22.5") {
		t.Errorf("expected temperature, got: %s", result)
	}
	if !strings.Contains(result, "feels like 21.0") {
		t.Errorf("expected apparent temperature, got: %s", result)
	}
}

func TestToolWeatherForecast(t *testing.T) {
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"latitude": 35.6895, "longitude": 139.6917, "name": "Tokyo", "country": "Japan"},
			},
		})
	}))
	defer geoSrv.Close()

	wxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"daily": map[string]any{
				"time":                          []string{"2026-02-23", "2026-02-24"},
				"weather_code":                  []int{0, 61},
				"temperature_2m_max":            []float64{15.0, 12.0},
				"temperature_2m_min":            []float64{5.0, 3.0},
				"precipitation_sum":             []float64{0.0, 5.2},
				"precipitation_probability_max": []float64{10.0, 80.0},
			},
			"daily_units": map[string]any{
				"temperature_2m_max": "°C",
			},
		})
	}))
	defer wxSrv.Close()

	origGeo := geocodingBaseURL
	origWx := weatherBaseURL
	geocodingBaseURL = geoSrv.URL
	weatherBaseURL = wxSrv.URL
	defer func() {
		geocodingBaseURL = origGeo
		weatherBaseURL = origWx
	}()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"location": "Tokyo", "days": 2})
	result, err := toolWeatherForecast(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "2-day forecast") {
		t.Errorf("expected forecast header, got: %s", result)
	}
	if !strings.Contains(result, "Clear sky") {
		t.Errorf("expected Clear sky, got: %s", result)
	}
	if !strings.Contains(result, "Slight rain") {
		t.Errorf("expected Slight rain, got: %s", result)
	}
	if !strings.Contains(result, "80%") {
		t.Errorf("expected precipitation probability, got: %s", result)
	}
}

func TestToolWeatherMissingLocation(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{})
	_, err := toolWeatherCurrent(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing location")
	}
	if !strings.Contains(err.Error(), "location required") {
		t.Errorf("expected location required error, got: %v", err)
	}
}

func TestToolWeatherForecastMissingLocation(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{})
	_, err := toolWeatherForecast(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing location")
	}
}

func TestToolWeatherDefaultLocation(t *testing.T) {
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the query contains the default location.
		if !strings.Contains(r.URL.RawQuery, "Osaka") {
			t.Errorf("expected query to contain Osaka, got: %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"latitude": 34.6937, "longitude": 135.5023, "name": "Osaka", "country": "Japan"},
			},
		})
	}))
	defer geoSrv.Close()

	wxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"current": map[string]any{
				"temperature_2m":       18.0,
				"relative_humidity_2m": 55.0,
				"weather_code":         2,
				"wind_speed_10m":       8.0,
				"apparent_temperature": 17.0,
			},
			"current_units": map[string]any{
				"temperature_2m": "°C",
				"wind_speed_10m": "km/h",
			},
		})
	}))
	defer wxSrv.Close()

	origGeo := geocodingBaseURL
	origWx := weatherBaseURL
	geocodingBaseURL = geoSrv.URL
	weatherBaseURL = wxSrv.URL
	defer func() {
		geocodingBaseURL = origGeo
		weatherBaseURL = origWx
	}()

	cfg := &Config{Weather: WeatherConfig{Location: "Osaka"}}
	input, _ := json.Marshal(map[string]string{}) // No location in input.
	result, err := toolWeatherCurrent(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Osaka") {
		t.Errorf("expected Osaka in result, got: %s", result)
	}
}

func TestToolWeatherAPIError(t *testing.T) {
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"latitude": 35.6895, "longitude": 139.6917, "name": "Tokyo", "country": "Japan"},
			},
		})
	}))
	defer geoSrv.Close()

	wxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer wxSrv.Close()

	origGeo := geocodingBaseURL
	origWx := weatherBaseURL
	geocodingBaseURL = geoSrv.URL
	weatherBaseURL = wxSrv.URL
	defer func() {
		geocodingBaseURL = origGeo
		weatherBaseURL = origWx
	}()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"location": "Tokyo"})
	_, err := toolWeatherCurrent(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestGeocodeLocationNotFound(t *testing.T) {
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []any{},
		})
	}))
	defer geoSrv.Close()

	origGeo := geocodingBaseURL
	geocodingBaseURL = geoSrv.URL
	defer func() { geocodingBaseURL = origGeo }()

	_, _, _, err := geocodeLocation("NonexistentPlace12345")
	if err == nil {
		t.Fatal("expected error for not found location")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}
