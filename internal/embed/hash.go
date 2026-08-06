package embed

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

const Dim = 384

// HashEmbedder is a deterministic offline bag-of-words feature-hash embedder.
// Unigram-heavy so paraphrases with shared content words score high; distinct
// intents that only share stop/filler words stay lower.
type HashEmbedder struct {
	Dim int
}

func NewHashEmbedder() *HashEmbedder {
	return &HashEmbedder{Dim: Dim}
}

func (e *HashEmbedder) Embed(text string) []float32 {
	dim := e.Dim
	if dim <= 0 {
		dim = Dim
	}
	v := make([]float32, dim)
	add := func(tok string, w float32) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum32()
		idx := int(sum % uint32(dim))
		sign := float32(1)
		if sum&1 == 1 {
			sign = -1
		}
		v[idx] += sign * w
	}

	tokens := tokenize(text)
	for _, tok := range tokens {
		if isStop(tok) {
			continue
		}
		add("w:"+tok, 1)
	}
	// Light bigram signal — keep low so word-order paraphrases still match.
	for i := 0; i+1 < len(tokens); i++ {
		if isStop(tokens[i]) || isStop(tokens[i+1]) {
			continue
		}
		add("b:"+tokens[i]+"_"+tokens[i+1], 0.25)
	}
	return l2normalize(v)
}

func isStop(t string) bool {
	switch t {
	case "a", "an", "the", "is", "are", "am", "to", "of", "and", "or", "on", "in", "for", "my", "i", "do", "how", "what", "steps", "with", "that", "this", "it", "me", "please":
		return true
	default:
		return false
	}
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var b strings.Builder
	var out []string
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func l2normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	n := math.Sqrt(sum)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}

func Cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var s float64
	for i := 0; i < n; i++ {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}
