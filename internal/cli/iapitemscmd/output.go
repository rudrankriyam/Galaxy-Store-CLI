package iapitemscmd

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/items"
)

type listOutput struct {
	*items.ListResult
}

func (result listOutput) OutputHeaders() []string {
	return itemHeaders()
}

func (result listOutput) OutputRows() [][]string {
	if result.ListResult == nil {
		return nil
	}
	return itemRows(result.Items)
}

type itemOutput struct {
	Item *items.Item
}

func (result itemOutput) MarshalJSON() ([]byte, error) {
	if result.Item == nil {
		return []byte("null"), nil
	}
	return json.Marshal(result.Item)
}

func (result itemOutput) OutputHeaders() []string {
	return itemHeaders()
}

func (result itemOutput) OutputRows() [][]string {
	if result.Item == nil {
		return nil
	}
	return itemRows([]items.Item{*result.Item})
}

type mutationPlanOutput struct {
	Action      string      `json:"action"`
	PackageName string      `json:"packageName"`
	ItemID      string      `json:"itemId"`
	DryRun      bool        `json:"dryRun"`
	Plan        shared.Plan `json:"plan"`
}

func newMutationPlan(action string, packageName string, itemID string) mutationPlanOutput {
	return mutationPlanOutput{
		Action:      action,
		PackageName: packageName,
		ItemID:      itemID,
		DryRun:      true,
		Plan: shared.Plan{
			Operations: []shared.Operation{{
				Action:   action,
				Resource: "Samsung IAP item " + itemID,
				Details:  "package " + packageName,
			}},
			Warnings: []string{
				"IAP Publish API mutations take effect immediately, including while an app is for sale.",
			},
			RequiresConfirmation: true,
			MutationsPerformed:   false,
		},
	}
}

func (result mutationPlanOutput) OutputHeaders() []string {
	return []string{"ACTION", "PACKAGE", "ITEM ID", "STATUS"}
}

func (result mutationPlanOutput) OutputRows() [][]string {
	return [][]string{{result.Action, result.PackageName, result.ItemID, "planned"}}
}

func itemHeaders() []string {
	return []string{"ITEM ID", "TITLE", "TYPE", "STATUS", "USD PRICE", "TERRITORIES"}
}

func itemRows(records []items.Item) [][]string {
	rows := make([][]string, len(records))
	for index, item := range records {
		territories := make([]string, 0, len(item.Prices))
		for _, price := range item.Prices {
			if price.CountryID != "" {
				territories = append(territories, price.CountryID)
			}
		}
		usdPrice := item.USDPrice.String()
		if usdPrice == "" {
			usdPrice = "-"
		}
		territorySummary := strings.Join(territories, ",")
		if territorySummary == "" && len(item.Prices) > 0 {
			territorySummary = strconv.Itoa(len(item.Prices))
		}
		rows[index] = []string{
			item.ID,
			item.Title,
			item.Type,
			item.Status,
			usdPrice,
			territorySummary,
		}
	}
	return rows
}

var (
	_ output.RowSource = listOutput{}
	_ output.RowSource = itemOutput{}
	_ output.RowSource = mutationPlanOutput{}
	_ json.Marshaler   = itemOutput{}
)
