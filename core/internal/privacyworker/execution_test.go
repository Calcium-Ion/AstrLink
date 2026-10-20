package privacyworker

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestInstallationExecutionMatchesTheWorkerContract(t *testing.T) {
	text := func(value string) *string { return &value }
	manifest := func(execution *installationExecution) installationManifest {
		return installationManifest{
			Adapter: contract.PrivacyModelAdapterHFToken, Window: 512,
			Execution: execution,
		}
	}
	coreML := func(units string) *installationExecution {
		return &installationExecution{
			Provider: "coreml", ModelFormat: text("mlprogram"),
			ComputeUnits: text(units), StaticSequenceLength: 512, PadTokenID: 1,
		}
	}
	valid := []*installationExecution{
		nil,
		coreML("cpu_and_gpu"), coreML("cpu_and_neural_engine"), coreML("all"),
		{Provider: "cpu", StaticSequenceLength: 512},
	}
	for _, execution := range valid {
		if err := validateInstallationExecution(manifest(execution)); err != nil {
			t.Fatalf("%#v: %v", execution, err)
		}
	}
	invalid := []*installationExecution{
		coreML("cpu_only"),
		{Provider: "coreml", ComputeUnits: text("all"), StaticSequenceLength: 512},
		{Provider: "coreml", ModelFormat: text("mlprogram"), StaticSequenceLength: 512},
		{Provider: "coreml", ModelFormat: text("neural_network"), ComputeUnits: text("all"), StaticSequenceLength: 512},
		{Provider: "cpu", ComputeUnits: text("all"), StaticSequenceLength: 512},
		{Provider: "cuda", StaticSequenceLength: 512},
		{Provider: "cpu", StaticSequenceLength: 256},
		{Provider: "cpu", StaticSequenceLength: 512, PadTokenID: -1},
		{Provider: "cpu", StaticSequenceLength: 512, PadTokenID: maxPadTokenID + 1},
	}
	for _, execution := range invalid {
		if validateInstallationExecution(manifest(execution)) == nil {
			t.Fatalf("accepted %#v", execution)
		}
	}
	openAI := manifest(coreML("all"))
	openAI.Adapter = contract.PrivacyModelAdapterOpenAIBIOES
	if validateInstallationExecution(openAI) == nil {
		t.Fatal("accepted Core ML for the OpenAI adapter")
	}
}
