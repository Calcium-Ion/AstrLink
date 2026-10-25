package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

const classifierTestKey = "sk-classifier-test-key-0123456789"

// The settings document and every API response carry only the credential
// reference; the API key exists solely as a sealed row.
func TestClassifierSettingsAndCredentialSealing(t *testing.T) {
	store := openTestStore(t, t.TempDir()+"/classifier.db")
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	empty, err := store.GetClassifierSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Enabled || empty.Inline != nil {
		t.Fatalf("default settings = %+v", empty)
	}

	if err := store.Put(ctx, secretstore.Ref(contract.ClassifierCredentialRef), []byte(classifierTestKey)); err != nil {
		t.Fatal(err)
	}
	timeout := 1500
	settings := contract.ClassifierSettings{
		Enabled:          true,
		ClassifierSource: contract.ClassifierSourceInline,
		Inline: &contract.ClassifierInlineSource{
			Protocol:      contract.ClassifierProtocolJev,
			Endpoint:      "https://api.typesafe.ai/v1/systemone",
			CredentialRef: contract.ClassifierCredentialRef,
			Model:         "jev-latest",
			TimeoutMS:     &timeout,
		},
	}
	if err := store.UpdateClassifierSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}

	var document string
	if err := store.db.QueryRow(`SELECT document_json FROM classifier_settings WHERE id = 1`).Scan(&document); err != nil {
		t.Fatal(err)
	}
	if contains := documentContainsKey(document, classifierTestKey); contains {
		t.Fatalf("settings document leaks the API key: %s", document)
	}

	var sealedValue []byte
	var sealedFlag int
	if err := store.db.QueryRow(`SELECT credential_value, sealed FROM classifier_credentials WHERE id = 1`).Scan(&sealedValue, &sealedFlag); err != nil {
		t.Fatal(err)
	}
	if sealedFlag != 1 {
		t.Fatalf("credential sealed = %d, want 1", sealedFlag)
	}
	if documentContainsKey(string(sealedValue), classifierTestKey) {
		t.Fatalf("credential value stored as plaintext: %q", sealedValue)
	}

	configured, err := store.ClassifierCredentialConfigured(ctx)
	if err != nil || !configured {
		t.Fatalf("configured = %v, %v", configured, err)
	}

	secret, err := store.Get(ctx, secretstore.Ref(contract.ClassifierCredentialRef))
	defer clear(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(secret) != classifierTestKey {
		t.Fatalf("unsealed credential = %q", secret)
	}

	loaded, err := store.GetClassifierSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Enabled || loaded.Inline == nil || loaded.Inline.Model != "jev-latest" || *loaded.Inline.TimeoutMS != 1500 {
		t.Fatalf("loaded settings = %+v", loaded)
	}

	if err := store.Delete(ctx, secretstore.Ref(contract.ClassifierCredentialRef)); err != nil {
		t.Fatal(err)
	}
	configured, err = store.ClassifierCredentialConfigured(ctx)
	if err != nil || configured {
		t.Fatalf("configured after delete = %v, %v", configured, err)
	}
	// Settings survive credential deletion; routing degrades, not the document.
	if _, err := store.GetClassifierSettings(ctx); err != nil {
		t.Fatalf("settings after credential delete: %v", err)
	}
}

func documentContainsKey(document, key string) bool {
	return strings.Contains(document, key)
}

// AAD binding: a ciphertext lifted onto another secret row must not open.
func TestClassifierCredentialAADBinding(t *testing.T) {
	store := openTestStore(t, t.TempDir()+"/classifier-aad.db")
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.Put(ctx, secretstore.Ref(contract.ClassifierCredentialRef), []byte(classifierTestKey)); err != nil {
		t.Fatal(err)
	}
	var sealedValue []byte
	if err := store.db.QueryRow(`SELECT credential_value FROM classifier_credentials`).Scan(&sealedValue); err != nil {
		t.Fatal(err)
	}
	// Copy the classifier ciphertext onto a builtin tool row under the same key
	// material; the AAD binds the table, so the read must fail.
	if _, err := store.db.Exec(`INSERT INTO builtin_tool_credentials (kind, credential_value, sealed) VALUES ('web_search', ?, 1)`, sealedValue); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(ctx, secretstore.Ref("local://builtin-tool/web_search"))
	defer clear(value)
	if err == nil {
		t.Fatal("ciphertext copied across tables opened; AAD binding is broken")
	}
}

func TestClassifierSettingsRejectInvalidDocuments(t *testing.T) {
	store := openTestStore(t, t.TempDir()+"/classifier-invalid.db")
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.UpdateClassifierSettings(ctx, contract.ClassifierSettings{
		Enabled:          true,
		ClassifierSource: contract.ClassifierSourceProviderRef,
		ProviderRef:      &contract.ClassifierProviderRef{Ref: "local://classifier-profile/x"},
	}); err == nil {
		t.Fatal("enabled provider_ref document must be rejected")
	}
}
