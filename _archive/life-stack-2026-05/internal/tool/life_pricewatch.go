package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"tetora/internal/life/pricewatch"
)

// PriceWatch handles the price_watch tool.
func PriceWatch(engine *pricewatch.Service, input json.RawMessage) (string, error) {
	var args struct {
		Action        string  `json:"action"`
		From          string  `json:"from"`
		To            string  `json:"to"`
		Condition     string  `json:"condition"`
		Threshold     float64 `json:"threshold"`
		ID            int     `json:"id"`
		UserID        string  `json:"userId"`
		NotifyChannel string  `json:"notifyChannel"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "add":
		err := engine.AddWatch(args.UserID, args.From, args.To, args.Condition, args.Threshold, args.NotifyChannel)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Price watch added: alert when %s/%s %s %.4f",
			strings.ToUpper(args.From), strings.ToUpper(args.To), args.Condition, args.Threshold), nil

	case "list":
		watches, err := engine.ListWatches(args.UserID)
		if err != nil {
			return "", err
		}
		if len(watches) == 0 {
			return "No price watches configured.", nil
		}
		out, _ := json.MarshalIndent(watches, "", "  ")
		return string(out), nil

	case "cancel":
		if args.ID <= 0 {
			return "", fmt.Errorf("id is required for cancel action")
		}
		err := engine.CancelWatch(args.ID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Price watch #%d cancelled.", args.ID), nil

	default:
		return "", fmt.Errorf("unknown action %q (use: add, list, cancel)", args.Action)
	}
}
