package answers

import (
	"testing"
)

func TestSuggestSkillsLeavesOutWhatTheUserHasInAnySpelling(t *testing.T) {
	got := SuggestSkills([]string{"Golang", "postgres", "React"}, nil, nil)
	for _, s := range got {
		switch s.Name {
		case "Go", "PostgreSQL", "React":
			t.Errorf("%s is already held, under another spelling, and should not be suggested", s.Name)
		case "Golang", "Postgres", "Node":
			t.Errorf("%s is an alias and should never be suggested on its own", s.Name)
		}
	}
	if len(got) == 0 {
		t.Fatal("the rest of the vocabulary should still be suggested")
	}
}

func TestSuggestSkillsRanksTheCVThenJobDemand(t *testing.T) {
	jobs := []string{
		"We run Kafka and Kubernetes. Kafka everywhere, Kafka all day.",
		"Kubernetes, Terraform and Kafka.",
		"Kubernetes on AWS.",
	}
	got := SuggestSkills(nil, []string{"terraform", "rust"}, jobs)
	if len(got) < 4 {
		t.Fatalf("expected the whole vocabulary, got %d suggestions", len(got))
	}
	top := []string{got[0].Name, got[1].Name, got[2].Name, got[3].Name}
	want := []string{"Terraform", "Rust", "Kubernetes", "Kafka"}
	for i := range want {
		if top[i] != want[i] {
			t.Fatalf("top suggestions = %v, want %v: CV skills first, then by how many jobs ask", top, want)
		}
	}
	if got[0].Jobs != 1 || !got[0].InCV {
		t.Errorf("Terraform: got %+v, want it marked as in the CV and asked for by 1 job", got[0])
	}
	if got[2].Jobs != 3 {
		t.Errorf("Kubernetes is mentioned in 3 jobs, got %d", got[2].Jobs)
	}
	// Jobs counts postings, not mentions: four "Kafka"s across two postings is 2.
	if got[3].Jobs != 2 {
		t.Errorf("Kafka is mentioned in 2 jobs, got %d", got[3].Jobs)
	}
}
