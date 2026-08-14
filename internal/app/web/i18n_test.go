package web

import "testing"

// TestEveryTranslationKeyHasAllLanguages guards against a key added with only some languages —
// T's fallback-to-English behavior would hide that silently in the UI (a French page would just
// quietly show English for that one string) rather than failing loudly.
func TestEveryTranslationKeyHasAllLanguages(t *testing.T) {
	for key, strs := range translations {
		for _, lang := range []Lang{LangEN, LangPL, LangDE, LangFR} {
			if _, ok := strs[lang]; !ok {
				t.Errorf("translation key %q is missing a %q entry", key, lang)
			}
		}
	}
}

func TestTFallsBackToEnglishForUnknownLanguage(t *testing.T) {
	if got := T(Lang("es"), "nav.dashboard"); got != "Dashboard" {
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

func TestTReturnsGermanForKnownKey(t *testing.T) {
	if got := T(LangDE, "nav.dashboard"); got != "Übersicht" {
		t.Fatalf("expected the German translation, got %q", got)
	}
}

func TestTReturnsFrenchForKnownKey(t *testing.T) {
	if got := T(LangFR, "nav.dashboard"); got != "Tableau de bord" {
		t.Fatalf("expected the French translation, got %q", got)
	}
}

func TestActionLabelTranslatesKnownActions(t *testing.T) {
	if got := actionLabel(LangPL, "BUY"); got != "KUPNO" {
		t.Fatalf("expected KUPNO, got %q", got)
	}
	if got := actionLabel(LangEN, "BUY"); got != "BUY" {
		t.Fatalf("expected BUY, got %q", got)
	}
	if got := actionLabel(LangDE, "BUY"); got != "KAUFEN" {
		t.Fatalf("expected KAUFEN, got %q", got)
	}
	if got := actionLabel(LangFR, "BUY"); got != "ACHETER" {
		t.Fatalf("expected ACHETER, got %q", got)
	}
}

func TestActionLabelPassesThroughUnknownActions(t *testing.T) {
	if got := actionLabel(LangPL, "SOMETHING_NEW"); got != "SOMETHING_NEW" {
		t.Fatalf("expected an unrecognized action to pass through unchanged, got %q", got)
	}
}

func TestValidLang(t *testing.T) {
	if !ValidLang(LangEN) || !ValidLang(LangPL) || !ValidLang(LangDE) || !ValidLang(LangFR) {
		t.Fatal("expected en, pl, de, and fr to all be valid")
	}
	if ValidLang(Lang("es")) || ValidLang(Lang("")) {
		t.Fatal("expected an unsupported language to be invalid")
	}
}
