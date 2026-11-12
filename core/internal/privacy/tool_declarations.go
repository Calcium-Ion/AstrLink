package privacy

// InspectionOptions selects which tool declarations extraction reaches. The
// same options must be used to extract a body and to locate its findings, or
// segment indexes no longer line up.
type InspectionOptions struct {
	InspectToolDeclarations bool
	SkipAdditionalTools     bool
}

func (policy Policy) InspectionOptions() InspectionOptions {
	return InspectionOptions{
		InspectToolDeclarations: policy.InspectToolDeclarations,
		SkipAdditionalTools:     policy.SkipAdditionalTools,
	}
}
