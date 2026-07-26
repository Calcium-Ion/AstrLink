package privacymodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

const fakeRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeHFRepository struct {
	t                 *testing.T
	repoID            string
	requestedRevision string
	revision          string
	assets            map[string][]byte
	lfs               map[string]bool
	downloadOverride  map[string][]byte
	transientFailures map[string]int
	truncateFailures  map[string]int
	assetAttempts     map[string]int
	blockAsset        string
	blockStarted      chan struct{}
	redirectAsset     string
	redirectURL       string
	metadataRequests  atomic.Int64
	assetRequests     atomic.Int64
	mu                sync.Mutex
	escapedMetadata   string
	metadataQuery     string
	server            *httptest.Server
}

func newFakeHFRepository(t *testing.T) *fakeHFRepository {
	t.Helper()
	repository := &fakeHFRepository{
		t:                 t,
		repoID:            "acme/privacy-tiny",
		requestedRevision: "refs/pr/1",
		revision:          fakeRevision,
		assets: map[string][]byte{
			"config.json": []byte(`{
				"architectures":["TinyForTokenClassification"],
				"type_vocab_size":2,
				"id2label":{"0":"O","1":"B-EMAIL","2":"I-EMAIL"}
			}`),
			"tokenizer.json":         []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
			"model_int8.onnx":        []byte("tiny fake onnx model"),
			"model_int8.onnx_data_0": []byte("external tensor data"),
			"model_fp32.onnx":        []byte("non-lfs model"),
			"gpu/model_fp16.onnx":    []byte("fake f16 model"),
		},
		lfs: map[string]bool{
			"model_int8.onnx":        true,
			"model_int8.onnx_data_0": true,
			"gpu/model_fp16.onnx":    true,
		},
		downloadOverride:  make(map[string][]byte),
		transientFailures: make(map[string]int),
		truncateFailures:  make(map[string]int),
		assetAttempts:     make(map[string]int),
	}
	repository.server = httptest.NewServer(http.HandlerFunc(repository.serveHTTP))
	t.Cleanup(repository.server.Close)
	return repository
}

func (repository *fakeHFRepository) serveHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if strings.HasPrefix(request.URL.Path, "/api/models/") {
		repository.metadataRequests.Add(1)
		repository.mu.Lock()
		repository.escapedMetadata = request.URL.EscapedPath()
		repository.metadataQuery = request.URL.RawQuery
		repository.mu.Unlock()
		repository.writeMetadata(writer)
		return
	}
	prefix := "/" + repository.repoID + "/resolve/" +
		repository.revision + "/"
	if strings.HasPrefix(request.URL.Path, prefix) {
		repository.assetRequests.Add(1)
		filename := strings.TrimPrefix(request.URL.Path, prefix)
		document, exists := repository.assets[filename]
		if !exists {
			http.NotFound(writer, request)
			return
		}
		repository.mu.Lock()
		repository.assetAttempts[filename]++
		transientFailure := repository.transientFailures[filename] > 0
		if transientFailure {
			repository.transientFailures[filename]--
		}
		truncateFailure := !transientFailure &&
			repository.truncateFailures[filename] > 0
		if truncateFailure {
			repository.truncateFailures[filename]--
		}
		repository.mu.Unlock()
		if transientFailure {
			http.Error(writer, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		if filename == repository.blockAsset {
			if repository.blockStarted != nil {
				select {
				case <-repository.blockStarted:
				default:
					close(repository.blockStarted)
				}
			}
			<-request.Context().Done()
			return
		}
		if filename == repository.redirectAsset {
			http.Redirect(
				writer,
				request,
				repository.redirectURL,
				http.StatusFound,
			)
			return
		}
		if override, ok := repository.downloadOverride[filename]; ok {
			document = override
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Header().Set("Content-Length", fmt.Sprint(len(document)))
		writer.WriteHeader(http.StatusOK)
		if truncateFailure {
			_, _ = writer.Write(document[:len(document)/2])
			return
		}
		_, _ = writer.Write(document)
		return
	}
	http.NotFound(writer, request)
}

func (repository *fakeHFRepository) assetAttemptCount(filename string) int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.assetAttempts[filename]
}

func (repository *fakeHFRepository) writeMetadata(writer http.ResponseWriter) {
	type lfsMetadata struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	type siblingMetadata struct {
		Filename string       `json:"rfilename"`
		Size     int64        `json:"size"`
		LFS      *lfsMetadata `json:"lfs,omitempty"`
	}
	names := make([]string, 0, len(repository.assets))
	for name := range repository.assets {
		names = append(names, name)
	}
	sort.Strings(names)
	siblings := make([]siblingMetadata, 0, len(names))
	for _, name := range names {
		document := repository.assets[name]
		sibling := siblingMetadata{
			Filename: name,
			Size:     int64(len(document)),
		}
		if repository.lfs[name] {
			sibling.LFS = &lfsMetadata{
				SHA256: testSHA256(document),
				Size:   int64(len(document)),
			}
		}
		siblings = append(siblings, sibling)
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"id":      repository.repoID,
		"modelId": repository.repoID,
		"sha":     repository.revision,
		"cardData": map[string]any{
			"license":  "apache-2.0",
			"language": []string{"en"},
		},
		"siblings": siblings,
	})
}

func (repository *fakeHFRepository) probe(t *testing.T) *hfProbe {
	t.Helper()
	probe, err := newHFProbe(
		repository.server.URL,
		repository.server.Client(),
		true,
	)
	if err != nil {
		t.Fatalf("newHFProbe: %v", err)
	}
	return probe
}

func TestHFProbePinsJSONAndLFSAssetsAndEscapesSlashRevision(t *testing.T) {
	repository := newFakeHFRepository(t)
	result, err := repository.probe(t).inspect(
		context.Background(),
		contract.PrivacyModelProbeRequest{
			RepoID:   repository.repoID,
			Revision: repository.requestedRevision,
		},
	)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	repository.mu.Lock()
	escapedMetadata := repository.escapedMetadata
	metadataQuery := repository.metadataQuery
	repository.mu.Unlock()
	if !strings.Contains(escapedMetadata, "refs%2Fpr%2F1") {
		t.Fatalf("metadata revision was not escaped atomically: %q", escapedMetadata)
	}
	if metadataQuery != "blobs=true" {
		t.Fatalf("metadata query = %q, want blobs=true", metadataQuery)
	}
	response := result.response
	if response.Revision != repository.revision ||
		response.License == nil ||
		*response.License != "apache-2.0" ||
		response.Adapter != contract.PrivacyModelAdapterHFToken ||
		response.RequiresLabelMapping ||
		len(response.Labels) != 1 ||
		response.Labels[0].SuggestedKind == nil ||
		*response.Labels[0].SuggestedKind != contract.CanonicalKindEmail {
		t.Fatalf("probe response = %#v", response)
	}
	plan, exists := result.plans["cpu_int8"]
	if !exists || !plan.variant.Supported ||
		plan.runtime.inputNames.TokenTypeIDs == nil ||
		*plan.runtime.inputNames.TokenTypeIDs != "token_type_ids" ||
		len(plan.runtime.externalData) != 1 ||
		plan.runtime.externalData[0] != "model_int8.onnx_data_0" {
		t.Fatalf("cpu_int8 plan = %#v, exists=%v", plan, exists)
	}
	if len(plan.assets) != 4 {
		t.Fatalf("asset count = %d, want 4", len(plan.assets))
	}
	for _, asset := range plan.assets {
		if !validPinnedAsset(asset) {
			t.Fatalf("asset was not pinned: %#v", asset)
		}
	}
	assertProbeVariant(t, response.Variants, "gpu_f16", false)
	assertProbeVariant(t, response.Variants, "cpu_fp32", false)
}

func TestSuggestedCanonicalKindRecognizesBuiltinPrivateLabels(t *testing.T) {
	expected := map[string]contract.CanonicalKind{
		"PRIVATE_EMAIL":   contract.CanonicalKindEmail,
		"PRIVATE_PHONE":   contract.CanonicalKindPhone,
		"PRIVATE_URL":     contract.CanonicalKindURL,
		"PRIVATE_ADDRESS": contract.CanonicalKindAddress,
		"PRIVATE_DATE":    contract.CanonicalKindDate,
		"PRIVATE_PERSON":  contract.CanonicalKindPerson,
	}
	for label, want := range expected {
		got := suggestedCanonicalKind(label)
		if got == nil || *got != want {
			t.Fatalf("suggestedCanonicalKind(%q)=%v, want %q", label, got, want)
		}
	}
}

func TestSafeAssetPathMatchesWorkerBoundary(t *testing.T) {
	maximum := strings.Repeat("a", 507) + ".onnx"
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: maximum, want: true},
		{path: "nested/model.onnx", want: true},
		{path: maximum + "x", want: false},
		{path: "model\x00.onnx", want: false},
		{path: "model\n.onnx", want: false},
	} {
		if got := safeAssetPath(test.path); got != test.want {
			t.Fatalf(
				"safeAssetPath(%q)=%t, want %t",
				test.path,
				got,
				test.want,
			)
		}
	}
}

func TestSourceDescriptorRejectsWorkerUnsafePaths(t *testing.T) {
	valid := testSourceDescriptor()
	if err := validateSourceDescriptor(valid); err != nil {
		t.Fatalf("valid 512-byte descriptor path: %v", err)
	}
	international := valid
	international.Name = strings.Repeat("隐", 50)
	international.License = strings.Repeat("许", 20)
	international.Languages = []string{"中文"}
	international.Variants = append(
		[]sourceDescriptorVariant(nil),
		valid.Variants...,
	)
	international.Variants[0].Name = strings.Repeat("量", 30)
	if err := validateSourceDescriptor(international); err != nil {
		t.Fatalf("valid rune-counted descriptor metadata: %v", err)
	}
	for _, invalidPath := range []string{
		strings.Repeat("a", 508) + ".onnx",
		"model\x00.onnx",
		"model\n.onnx",
	} {
		candidate := valid
		candidate.Variants = append(
			[]sourceDescriptorVariant(nil),
			valid.Variants...,
		)
		candidate.Variants[0].ModelPath = invalidPath
		if err := validateSourceDescriptor(candidate); !errors.Is(
			err,
			ErrUnsupportedModel,
		) {
			t.Fatalf("unsafe path %q error=%v", invalidPath, err)
		}
	}
	for name, mutate := range map[string]func(*sourceDescriptor){
		"trimmed model name": func(value *sourceDescriptor) {
			value.Name = " Test Model"
		},
		"controlled variant name": func(value *sourceDescriptor) {
			value.Variants[0].Name = "CPU\nINT8"
		},
		"trimmed license": func(value *sourceDescriptor) {
			value.License = "MIT "
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Variants = append(
				[]sourceDescriptorVariant(nil),
				valid.Variants...,
			)
			mutate(&candidate)
			if err := validateSourceDescriptor(candidate); !errors.Is(
				err,
				ErrUnsupportedModel,
			) {
				t.Fatalf("invalid metadata error=%v", err)
			}
		})
	}
}

func TestSourceDescriptorCapsAssetsAndByteTotals(t *testing.T) {
	tooManyAssets := testSourceDescriptor()
	tooManyAssets.Variants[0].ModelPath = "model.onnx"
	tooManyAssets.Variants[0].ExternalData = make([]string, 126)
	for index := range tooManyAssets.Variants[0].ExternalData {
		tooManyAssets.Variants[0].ExternalData[index] = fmt.Sprintf(
			"model.onnx_data_%d",
			index,
		)
	}
	if err := validateSourceDescriptor(tooManyAssets); !errors.Is(
		err,
		ErrUnsupportedModel,
	) {
		t.Fatalf("excess descriptor assets error=%v", err)
	}

	overflow := testSourceDescriptor()
	overflow.Variants[0].ModelPath = "model.onnx"
	variants, plans := descriptorVariants(
		[]hfSibling{
			testLFSSibling(
				"model.onnx",
				contract.MaxPrivacyModelByteCount,
			),
			testLFSSibling("tokenizer.json", 1),
			testLFSSibling("config.json", 1),
		},
		overflow,
	)
	if len(variants) != 1 ||
		variants[0].Supported ||
		variants[0].BytesTotal != contract.MaxPrivacyModelByteCount ||
		variants[0].BytesTotal < 0 ||
		len(plans) != 1 {
		t.Fatalf("overflow variants=%#v plans=%#v", variants, plans)
	}
}

func testSourceDescriptor() sourceDescriptor {
	return sourceDescriptor{
		Version: 1, Name: "Test Model", License: "Apache-2.0",
		Languages: []string{"en"},
		Adapter:   contract.PrivacyModelAdapterHFToken,
		Variants: []sourceDescriptorVariant{{
			ID: "cpu_int8", Name: "CPU INT8", Quantization: "int8",
			EstimatedRAMBytes: 2,
			ModelPath:         strings.Repeat("a", 507) + ".onnx",
			ExternalData:      []string{},
			TokenizerPath:     "tokenizer.json",
			ConfigPath:        "config.json",
			TagScheme:         "bio",
			Window:            512,
			Stride:            128,
			MaxRequestTokens:  1024,
			InputNames: normalizedInputNames{
				InputIDs: "input_ids", AttentionMask: "attention_mask",
			},
			OutputName: "logits",
		}},
	}
}

func testLFSSibling(filename string, size int64) hfSibling {
	sibling := hfSibling{Filename: filename, Size: size}
	sibling.LFS = &struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}{
		SHA256: strings.Repeat("a", sha256.Size*2),
		Size:   size,
	}
	return sibling
}

func TestHFProbeRejectsInvalidTagSetAndUnmappableLabelSyntax(t *testing.T) {
	for _, config := range []string{
		`{"architectures":["TinyForTokenClassification"],"id2label":{"0":"o","1":"B-EMAIL","2":"I-EMAIL"}}`,
		`{"architectures":["TinyForTokenClassification"],"id2label":{"0":"O","1":"B-PER SON","2":"I-PER SON"}}`,
		`{"architectures":["TinyForTokenClassification"],"id2label":{"0":"O","1":"B-EMAIL"}}`,
	} {
		t.Run(testSHA256([]byte(config))[:8], func(t *testing.T) {
			repository := newFakeHFRepository(t)
			repository.assets["config.json"] = []byte(config)
			_, err := repository.probe(t).inspect(
				context.Background(),
				contract.PrivacyModelProbeRequest{
					RepoID:   repository.repoID,
					Revision: repository.requestedRevision,
				},
			)
			if err == nil {
				t.Fatal("invalid label configuration was accepted")
			}
		})
	}
}

func TestHFProbeStrictlyParsesSafeSourceDescriptorAndRejectsGLiNER(t *testing.T) {
	descriptor := `{
		"version":1,
		"name":"Custom Token Classifier",
		"license":"MIT",
		"languages":["en"],
		"adapter":"hf_token_classification",
		"variants":[{
			"id":"q4",
			"name":"CPU Q4",
			"quantization":"q4",
			"estimated_ram_bytes":268435456,
			"recommended":true,
			"model_path":"model_q4.onnx",
			"external_data_paths":[],
			"tokenizer_path":"tokenizer.json",
			"config_path":"config.json",
			"calibration_path":null,
			"tag_scheme":"bio",
			"window":512,
			"stride":0,
			"max_request_tokens":4096,
			"input_names":{
				"input_ids":"input_ids",
				"attention_mask":"attention_mask",
				"token_type_ids":null
			},
			"output_name":"logits"
		}]
	}`
	repository := newFakeHFRepository(t)
	repository.assets = map[string][]byte{
		InstallationManifestName: []byte(descriptor),
		"config.json": []byte(`{
			"id2label":{"0":"O","1":"B-EMAIL","2":"I-EMAIL"}
		}`),
		"tokenizer.json": []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
		"model_q4.onnx":  []byte("fake q4 model"),
	}
	repository.lfs = map[string]bool{"model_q4.onnx": true}
	result, err := repository.probe(t).inspect(
		context.Background(),
		contract.PrivacyModelProbeRequest{
			RepoID:   repository.repoID,
			Revision: repository.requestedRevision,
		},
	)
	if err != nil {
		t.Fatalf("descriptor inspect: %v", err)
	}
	plan, exists := result.plans["q4"]
	if !exists || !plan.variant.Supported ||
		plan.runtime.stride != 0 ||
		result.response.Name != "Custom Token Classifier" ||
		result.response.License == nil ||
		*result.response.License != "MIT" {
		t.Fatalf("descriptor result=%#v plan=%#v", result.response, plan)
	}

	repository.assets["config.json"] = []byte(`{
		"model_type":"gliner",
		"architectures":["GLiNER"],
		"id2label":{"0":"O","1":"B-EMAIL","2":"I-EMAIL"}
	}`)
	if _, err := repository.probe(t).inspect(
		context.Background(),
		contract.PrivacyModelProbeRequest{
			RepoID:   repository.repoID,
			Revision: repository.requestedRevision,
		},
	); !errorsIs(err, ErrUnsupportedModel) {
		t.Fatalf("GLiNER descriptor error=%v", err)
	}

	repository.assets["config.json"] = []byte(`{
		"id2label":{"0":"O","1":"B-EMAIL","2":"I-EMAIL"}
	}`)
	repository.assets[InstallationManifestName] = []byte(
		strings.Replace(descriptor, `"version":1`, `"version":1,"unknown":true`, 1),
	)
	if _, err := repository.probe(t).inspect(
		context.Background(),
		contract.PrivacyModelProbeRequest{
			RepoID:   repository.repoID,
			Revision: repository.requestedRevision,
		},
	); !errorsIs(err, ErrUnsupportedModel) {
		t.Fatalf("unknown descriptor field error=%v", err)
	}
}

func TestNoRemoteModelsGuardAllowsLoopbackAndRejectsRemoteAndRedirects(t *testing.T) {
	t.Setenv(noRemoteModelsEnvironment, "1")
	if _, err := newHFProbe("", nil, false); !errorsIs(err, ErrInvalidConfig) {
		t.Fatalf("default remote probe error = %v", err)
	}
	var remoteRoundTrips atomic.Int64
	redirectServer := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		http.Redirect(
			writer,
			request,
			"https://huggingface.co/api/models/acme/model",
			http.StatusFound,
		)
	}))
	t.Cleanup(redirectServer.Close)
	client := &http.Client{Transport: roundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		if !isLoopbackHostname(request.URL.Hostname()) {
			remoteRoundTrips.Add(1)
		}
		return http.DefaultTransport.RoundTrip(request)
	})}
	probe, err := newHFProbe(redirectServer.URL, client, true)
	if err != nil {
		t.Fatalf("loopback probe: %v", err)
	}
	_, err = probe.getJSON(
		context.Background(),
		redirectServer.URL,
		1024,
	)
	if err == nil {
		t.Fatal("redirect to remote host was accepted")
	}
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        t.TempDir(),
		MetadataBaseURL:      redirectServer.URL,
		HTTPClient:           client,
		TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatalf("loopback registry: %v", err)
	}
	if _, err := registry.Probe(
		context.Background(),
		contract.PrivacyModelProbeRequest{
			RepoID:   "acme/model",
			Revision: "main",
		},
	); err == nil {
		t.Fatal("registry Probe followed a remote redirect")
	}
	if _, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       "acme/model",
			Revision:     fakeRevision,
			VariantID:    "q4",
			LabelMapping: emailMapping(),
		},
	); err == nil {
		t.Fatal("registry Install followed a remote metadata redirect")
	}
	if remoteRoundTrips.Load() != 0 {
		t.Fatalf("remote round trips = %d, want 0", remoteRoundTrips.Load())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

func assertProbeVariant(
	t *testing.T,
	variants []contract.PrivacyModelVariant,
	id string,
	supported bool,
) {
	t.Helper()
	for _, variant := range variants {
		if variant.ID == id {
			if variant.Supported != supported {
				t.Fatalf("%s supported=%v, want %v", id, variant.Supported, supported)
			}
			if !supported && (variant.UnsupportedReason == nil ||
				*variant.UnsupportedReason != "cpu_only") {
				t.Fatalf("%s unsupported reason = %#v", id, variant.UnsupportedReason)
			}
			return
		}
	}
	t.Fatalf("variant %q not found in %#v", id, variants)
}

func testSHA256(document []byte) string {
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:])
}

func errorsIs(err, target error) bool {
	return err == target
}
