package cache

import "testing"

func TestSemanticRelatedRejectsUnrelatedPromptsAndVolatileSalts(t *testing.T) {
	query := "fast :: {} :: abcdef12 What are the steps to reset my dashboard password?"
	candidate := "fast :: {} :: abcdef12 How do I reset my password on the dashboard?"
	_, queryPrompt := cacheScopeAndPrompt(query)
	_, candidatePrompt := cacheScopeAndPrompt(candidate)
	if !semanticRelated(queryPrompt, candidatePrompt) {
		t.Fatal("expected reset-password paraphrase to be related")
	}

	unrelated := "fast :: {} :: abcdef12 Compare TCP and UDP for game servers."
	_, unrelatedPrompt := cacheScopeAndPrompt(unrelated)
	if semanticRelated(queryPrompt, unrelatedPrompt) {
		t.Fatal("unrelated prompt was considered semantically related")
	}
	_, differentSalt := cacheScopeAndPrompt(
		"fast :: {} :: 1234abcd What are the steps to reset my dashboard password?",
	)
	if semanticRelated(queryPrompt, differentSalt) {
		t.Fatal("cache-busting salt was ignored across requests")
	}
}

func TestCacheScopeSeparatesModels(t *testing.T) {
	key := Key("smart", "{}", "  Summarize   this ")
	scope, prompt := cacheScopeAndPrompt(key)
	if scope != "smart" || prompt != "summarize this" {
		t.Fatalf("scope=%q prompt=%q", scope, prompt)
	}
}
