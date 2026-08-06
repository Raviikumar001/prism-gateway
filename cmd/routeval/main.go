package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/raviikumar001/prism-gateway/internal/route"
)

type row struct {
	ID           string `json:"id"`
	ExpectedTier string `json:"expected_tier"`
	Prompt       string `json:"prompt"`
	Note         string `json:"note"`
}

type caseResult struct {
	ID             string `json:"id"`
	ExpectedTier   string `json:"expected_tier"`
	FeatureTier    string `json:"feature_tier"`
	LengthTier    string `json:"length_tier"`
	FeatureCorrect bool   `json:"feature_correct"`
	LengthCorrect  bool   `json:"length_correct"`
	RouteReason    string `json:"route_reason"`
	Note           string `json:"note,omitempty"`
}

func main() {
	path := "data/routing_eval.jsonl"
	jsonOut := ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-json":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "-json requires a path")
				os.Exit(1)
			}
			jsonOut = args[i+1]
			i++
		default:
			path = args[i]
		}
	}

	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	var featOK, lenOK, total int
	results := make([]caseResult, 0, 40)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			fmt.Fprintf(os.Stderr, "parse: %v\n", err)
			os.Exit(1)
		}
		total++
		got := route.Classify(r.Prompt, route.DefaultAutoThreshold)
		base := route.LengthOnlyBaseline(r.Prompt, 40)
		featHit := got.Tier == r.ExpectedTier
		lenHit := base == r.ExpectedTier
		if featHit {
			featOK++
		} else {
			fmt.Printf("MISS feature id=%s want=%s got=%s note=%s reason=%s\n", r.ID, r.ExpectedTier, got.Tier, r.Note, got.Reason)
		}
		if lenHit {
			lenOK++
		}
		results = append(results, caseResult{
			ID:             r.ID,
			ExpectedTier:   r.ExpectedTier,
			FeatureTier:    got.Tier,
			LengthTier:     base,
			FeatureCorrect: featHit,
			LengthCorrect:  lenHit,
			RouteReason:    got.Reason,
			Note:           r.Note,
		})
	}
	fmt.Printf("feature_accuracy=%.2f (%d/%d)\n", float64(featOK)/float64(total), featOK, total)
	fmt.Printf("length_accuracy=%.2f (%d/%d)\n", float64(lenOK)/float64(total), lenOK, total)

	if jsonOut != "" {
		payload := map[string]any{
			"cases":             results,
			"feature_correct":   featOK,
			"length_correct":    lenOK,
			"total":             total,
			"feature_accuracy":  float64(featOK) / float64(total),
			"length_accuracy":   float64(lenOK) / float64(total),
		}
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(jsonOut, raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", jsonOut)
	}

	if float64(featOK)/float64(total) <= float64(lenOK)/float64(total) {
		os.Exit(2)
	}
	if float64(featOK)/float64(total) < 0.85 {
		os.Exit(3)
	}
}
