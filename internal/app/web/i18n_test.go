package web

import "testing"

// TestEveryTranslationKeyHasBothLanguages guards against a key added with only one language —
// T's fallback-to-English behavior would hide that silently in the UI (a Polish page would just
// quietly show English for that one string) rather than failing loudly.
func TestEveryTranslationKeyHasBothLanguages(t *testing.T) {
	for key, strs := range translations {
		if _, ok := strs[LangEN]; !ok {
			t.Errorf("translation key %q is missing an English (LangEN) entry", key)
		}
		if _, ok := strs[LangPL]; !ok {
			t.Errorf("translation key %q is missing a Polish (LangPL) entry", key)
		}
	}
}

func TestTFallsBackToEnglishForUnknownLanguage(t *testing.T) {
	if got := T(Lang("fr"), "nav.dashboard"); got != "Dashboard" {
		t.Fatalf("expected an unrecognized language to fall back to English, got %q", got)
	}
}

func TestTFallsBackToKeyItselfWhenMissing(t *testing.T) {
	if got := T(LangEN, "does.not.exist"); got != "does.not.exist" {
		t.Fatalf("expected the raw key back for a missing translation, got %q", got)
	}
}

func TestTReturnsPolishForKnownKey(t *testing.T) {
	if got := T(LangPL, "nav.dashboard"); got != "Panel główny" {
		t.Fatalf("expected the Polish translation, got %q", got)
	}
}

func TestActionLabelTranslatesKnownActions(t *testing.T) {
	if got := actionLabel(LangPL, "BUY"); got != "KUPNO" {
		t.Fatalf("expected KUPNO, got %q", got)
	}
	if got := actionLabel(LangEN, "BUY"); got != "BUY" {
		t.Fatalf("expected BUY, got %q", got)
	}
}

func TestActionLabelPassesThroughUnknownActions(t *testing.T) {
	if got := actionLabel(LangPL, "SOMETHING_NEW"); got != "SOMETHING_NEW" {
		t.Fatalf("expected an unrecognized action to pass through unchanged, got %q", got)
	}
}

func TestValidLang(t *testing.T) {
	if !ValidLang(LangEN) || !ValidLang(LangPL) {
		t.Fatal("expected en and pl to both be valid")
	}
	if ValidLang(Lang("fr")) || ValidLang(Lang("")) {
		t.Fatal("expected an unsupported language to be invalid")
	}
}
