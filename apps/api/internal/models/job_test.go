package models

import "testing"

// The links a job is applied at and read at are decided here and nowhere
// else: the outbound endpoint applies at ApplyTarget, compose reads the form
// at FormTarget.
func TestJobTargets(t *testing.T) {
	const partner, employer, apply = "https://partner.example/p/1", "https://boards.example/acme/1", "https://aggregator.example/j/1"
	for _, c := range []struct {
		name                string
		job                 Job
		wantApply, wantForm string
	}{
		{"all three", Job{JobInput: JobInput{ApplyURL: apply}, EmployerURL: employer, PartnerApplyURL: partner}, partner, employer},
		{"no partner", Job{JobInput: JobInput{ApplyURL: apply}, EmployerURL: employer}, employer, employer},
		{"no employer page", Job{JobInput: JobInput{ApplyURL: apply}, PartnerApplyURL: partner}, partner, apply},
		{"apply URL only", Job{JobInput: JobInput{ApplyURL: apply}}, apply, apply},
		{"employer page only", Job{EmployerURL: employer}, employer, employer},
		{"partner only", Job{PartnerApplyURL: partner}, partner, ""},
		// A rejected match has its links withheld by the store: nothing to
		// apply at and no form to read.
		{"withheld", Job{}, "", ""},
	} {
		if got := c.job.ApplyTarget(); got != c.wantApply {
			t.Errorf("%s: ApplyTarget() = %q, want %q", c.name, got, c.wantApply)
		}
		if got := c.job.FormTarget(); got != c.wantForm {
			t.Errorf("%s: FormTarget() = %q, want %q", c.name, got, c.wantForm)
		}
	}
}
