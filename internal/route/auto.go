package route

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const DefaultAutoThreshold = 0.65

type rule struct {
	name string
	re   *regexp.Regexp
	w    float64
}

var smartRules = []rule{
	{"proof", regexp.MustCompile(`(?i)\b(prove|proof|disprove|irrational|theorem)\b`), 0.55},
	{"fermi", regexp.MustCompile(`(?i)\b(estimate how many|show your reasoning)\b`), 0.50},
	{"systems", regexp.MustCompile(`(?i)\b(distributed|consensus|quorum|network partition|p99|p95|leaderless|consistency guarantees)\b`), 0.45},
	{"design_heavy", regexp.MustCompile(`(?i)\b(design a|database schema|zero-downtime|migration plan|event sourcing|usage-based pricing)\b`), 0.50},
	{"compare_reco", regexp.MustCompile(`(?i)\b(compare .+ with|recommend one|with justification|explain your trade-?offs)\b`), 0.40},
	{"algo_hard", regexp.MustCompile(`(?i)\b(better than O\(|longest palindrom|dynamic programming|algorithm works)\b`), 0.50},
	{"debug_expert", regexp.MustCompile(`(?i)\b(most likely causes|doubled after a deploy|plausible mechanisms)\b`), 0.50},
	{"deep_why", regexp.MustCompile(`(?i)\bwhy can adding a cache increase\b`), 0.55},
}

var fastRules = []rule{
	{"roster", regexp.MustCompile(`(?i)\b(on[- ]?call roster|who is on call)\b`), 0.55},
	{"extract_log", regexp.MustCompile(`(?i)\b(timestamp of the first ERROR|extract the email)\b`), 0.55},
	{"translate", regexp.MustCompile(`(?i)\b(translate '|convert \d+ degrees|fahrenheit to celsius)\b`), 0.50},
	{"one_line", regexp.MustCompile(`(?i)\b(one-line commit|more polite without changing)\b`), 0.45},
	{"simple_fact", regexp.MustCompile(`(?i)^(what is the capital|what does HTTP status|is \d+ divisible)`), 0.40},
}

type Decision struct {
	Tier      string
	Score     float64
	Threshold float64
	Signals   []string
	Reason    string
}

func Classify(prompt string, threshold float64) Decision {
	if threshold <= 0 {
		threshold = DefaultAutoThreshold
	}
	p := strings.TrimSpace(prompt)
	score := 0.20
	var signals []string

	for _, r := range smartRules {
		if r.re.MatchString(p) {
			score += r.w
			signals = append(signals, "+"+r.name)
		}
	}
	for _, r := range fastRules {
		if r.re.MatchString(p) {
			score -= r.w
			signals = append(signals, "-"+r.name)
		}
	}

	words := countWords(p)
	// Long prompts without smart markers are usually extraction/roster traps.
	if words > 60 {
		smartHit := false
		for _, s := range signals {
			if strings.HasPrefix(s, "+") {
				smartHit = true
				break
			}
		}
		if !smartHit {
			score -= 0.35
			signals = append(signals, "-long_trivial")
		}
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	tier := "fast"
	if score >= threshold {
		tier = "smart"
	}
	reason := fmt.Sprintf("score=%.2f threshold=%.2f signals=%s → %s", score, threshold, strings.Join(signals, ","), tier)
	return Decision{Tier: tier, Score: score, Threshold: threshold, Signals: signals, Reason: reason}
}

func countWords(s string) int {
	n := 0
	in := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			in = false
			continue
		}
		if !in {
			n++
			in = true
		}
	}
	return n
}

func LengthOnlyBaseline(prompt string, wordThreshold int) string {
	if wordThreshold <= 0 {
		wordThreshold = 40
	}
	if countWords(prompt) >= wordThreshold {
		return "smart"
	}
	return "fast"
}
