// Command check validates the committed Galaxy Store CLI example fixtures.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/iap/items"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/stats"
)

func main() {
	root := flag.String("root", "examples", "path to the examples directory")
	flag.Parse()

	count, err := check(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "examples check:", err)
		os.Exit(1)
	}
	fmt.Printf("checked %d JSON fixtures and their command schemas\n", count)
}

func check(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if err := checkOneJSON(path); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, errors.New("no JSON fixtures found")
	}

	if _, err := metadata.ReadBundle(filepath.Join(root, "metadata", "bundle")); err != nil {
		return 0, fmt.Errorf("metadata bundle: %w", err)
	}
	if err := checkIAPFixtures(root); err != nil {
		return 0, err
	}
	if err := checkBetaAndRolloutFixtures(root); err != nil {
		return 0, err
	}
	if err := checkStatsFixtures(root); err != nil {
		return 0, err
	}
	return count, nil
}

func checkOneJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s: multiple JSON values", path)
		}
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func checkIAPFixtures(root string) error {
	createPath := filepath.Join(root, "iap", "item-create.json")
	var create items.FullRequest
	if err := readStrict(createPath, &create); err != nil {
		return err
	}
	if err := create.Validate(); err != nil {
		return fmt.Errorf("%s: %w", createPath, err)
	}

	updatePath := filepath.Join(root, "iap", "item-update.json")
	var update items.UpdateRequest
	if err := readStrict(updatePath, &update); err != nil {
		return err
	}
	if err := update.Validate(); err != nil {
		return fmt.Errorf("%s: %w", updatePath, err)
	}
	return nil
}

type betaFixture struct {
	AddTesters      []string `json:"betaTestersToBeAdded"`
	DeleteTesters   []string `json:"betaTestersToBeDeleted"`
	FeedbackChannel *string  `json:"feedbackChannel"`
}

type rolloutRateFixture struct {
	RolloutRate int `json:"rolloutRate"`
	Countries   []struct {
		CountryCode string `json:"countryCode"`
		RolloutRate int    `json:"rolloutRate"`
	} `json:"countries"`
}

type rolloutBinaryFixture struct {
	Function  string `json:"function"`
	BinarySeq string `json:"binarySeq"`
}

func checkBetaAndRolloutFixtures(root string) error {
	base := filepath.Join(root, "beta-rollout")
	var beta betaFixture
	if err := readStrict(filepath.Join(base, "testers.json"), &beta); err != nil {
		return err
	}
	if len(beta.AddTesters) == 0 &&
		len(beta.DeleteTesters) == 0 &&
		beta.FeedbackChannel == nil {
		return errors.New("beta tester fixture contains no update")
	}

	var rates rolloutRateFixture
	if err := readStrict(filepath.Join(base, "rates.json"), &rates); err != nil {
		return err
	}
	if rates.RolloutRate < 1 || rates.RolloutRate > 100 {
		return errors.New("rollout fixture rate must be between 1 and 100")
	}
	for _, country := range rates.Countries {
		if len(country.CountryCode) != 3 ||
			country.RolloutRate < 1 ||
			country.RolloutRate > 100 {
			return errors.New("rollout fixture contains an invalid country rate")
		}
	}

	var binary rolloutBinaryFixture
	if err := readStrict(filepath.Join(base, "binary.json"), &binary); err != nil {
		return err
	}
	if binary.Function != "ADD" && binary.Function != "REMOVE" {
		return errors.New("rollout binary fixture function must be ADD or REMOVE")
	}
	if binary.BinarySeq == "" {
		return errors.New("rollout binary fixture requires binarySeq")
	}
	return nil
}

type sellerStatsFixture struct {
	MetricIDs             []string       `json:"metricIds"`
	Periods               []stats.Period `json:"periods"`
	GetDailyMetric        bool           `json:"getDailyMetric"`
	GetBreakdownsByFilter bool           `json:"getBreakdownsByFilter"`
	NoContentMetadata     bool           `json:"noContentMetadata"`
	Filters               stats.Filters  `json:"filters"`
	TrendAggregation      string         `json:"trendAggregation"`
}

type contentStatsFixture struct {
	ContentID        string         `json:"contentId"`
	MetricIDs        []string       `json:"metricIds"`
	Periods          []stats.Period `json:"periods"`
	NoBreakdown      bool           `json:"noBreakdown"`
	Filters          stats.Filters  `json:"filters"`
	TrendAggregation string         `json:"trendAggregation"`
}

func checkStatsFixtures(root string) error {
	base := filepath.Join(root, "stats")
	var seller sellerStatsFixture
	if err := readStrict(filepath.Join(base, "seller.json"), &seller); err != nil {
		return err
	}
	if err := checkStatsQuery(seller.MetricIDs, seller.Periods, seller.TrendAggregation); err != nil {
		return fmt.Errorf("seller stats fixture: %w", err)
	}

	var content contentStatsFixture
	if err := readStrict(filepath.Join(base, "content.json"), &content); err != nil {
		return err
	}
	if len(content.ContentID) != 12 {
		return errors.New("content stats fixture requires a 12-digit contentId")
	}
	if err := checkStatsQuery(content.MetricIDs, content.Periods, content.TrendAggregation); err != nil {
		return fmt.Errorf("content stats fixture: %w", err)
	}
	return nil
}

func checkStatsQuery(metricIDs []string, periods []stats.Period, aggregation string) error {
	if len(metricIDs) == 0 || len(periods) == 0 {
		return errors.New("metrics and periods are required")
	}
	if aggregation != stats.AggregationDay &&
		aggregation != stats.AggregationWeek &&
		aggregation != stats.AggregationMonth {
		return errors.New("trendAggregation must be day, week, or month")
	}
	for _, period := range periods {
		start, startErr := time.Parse("2006-01-02", period.StartDate)
		end, endErr := time.Parse("2006-01-02", period.EndDate)
		if startErr != nil || endErr != nil || start.After(end) {
			return errors.New("period must contain an ordered YYYY-MM-DD range")
		}
	}
	return nil
}

func readStrict(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s: multiple JSON values", path)
		}
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
