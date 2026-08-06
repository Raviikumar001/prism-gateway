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

func main() {
	path := "data/routing_eval.jsonl"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	var featOK, lenOK, total int
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
		if got.Tier == r.ExpectedTier {
			featOK++
		} else {
			fmt.Printf("MISS feature id=%s want=%s got=%s note=%s reason=%s\n", r.ID, r.ExpectedTier, got.Tier, r.Note, got.Reason)
		}
		if base == r.ExpectedTier {
			lenOK++
		}
	}
	fmt.Printf("feature_accuracy=%.2f (%d/%d)\n", float64(featOK)/float64(total), featOK, total)
	fmt.Printf("length_accuracy=%.2f (%d/%d)\n", float64(lenOK)/float64(total), lenOK, total)
	if float64(featOK)/float64(total) <= float64(lenOK)/float64(total) {
		os.Exit(2)
	}
	if float64(featOK)/float64(total) < 0.85 {
		os.Exit(3)
	}
}
