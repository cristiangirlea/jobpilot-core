package models

// Extraction is what a posting asks of a candidate, as a model read it and
// code verified it against the posting's text.
type Extraction struct {
	Required    []ExtractedSkills     `json:"required"`
	NiceToHave  []ExtractedSkills     `json:"nice_to_have"`
	Stack       []ExtractedSkills     `json:"stack"`
	Seniority   ExtractedSeniority    `json:"seniority"`
	MinYears    *int                  `json:"min_years"`
	Constraints []ExtractedConstraint `json:"constraints"`

	// Dropped counts the items left out because their evidence failed.
	Dropped int `json:"dropped"`
}

// ExtractedSkills is one entry: any one of Skills meets it, and Quote is the
// posting's own words for it.
type ExtractedSkills struct {
	Skills []string `json:"skills"`
	Quote  string   `json:"quote"`
}

// ExtractedSeniority has a nil Quote exactly when Level is "unspecified".
type ExtractedSeniority struct {
	Level string  `json:"level"`
	Quote *string `json:"quote"`
}

// ExtractedConstraint is a condition on the candidate that is not a skill.
// Value is the model's wording, not the posting's; Quote is the posting's.
type ExtractedConstraint struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Hard  bool   `json:"hard"`
	Quote string `json:"quote"`
}
