package ats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Personio publishes no form API for applicants. Its careers pages,
// <company>.jobs.personio.de/job/<id>, are a Next.js site, and the page at
// /job/<id>/apply carries the job's whole form in the data it streams to the
// browser: every field with its label, type, required flag and options, and
// the document slots. The reader reads that data, read-only and only on
// Personio's own hosts. If the page stops carrying it, the form is unread,
// never the job closed.
var (
	personioPattern     = regexp.MustCompile(`(?i)^([a-z0-9-]+)\.jobs\.personio\.(?:de|com)$`)
	personioJobPath     = regexp.MustCompile(`^/job/(\d+)`)
	personioFlightChunk = regexp.MustCompile(`self\.__next_f\.push\(\[1,"((?:[^"\\]|\\.)*)"\]\)`)
	errPersonioUnusable = errors.New("Personio did not return this form, so it has to be completed on the employer's page")
)

// personioLabels names the standard fields the way other boards do, so the
// answer resolver recognises them; the page's own labels are terse ("First").
var personioLabels = map[string]string{
	"first_name": "First name", "last_name": "Last name", "email": "Email", "phone": "Phone",
	"available_from": "Available from", "salary_expectations": "Expected salary",
}

type personioForm struct {
	Fields []struct {
		Name      string          `json:"name"`
		Label     string          `json:"label"`
		Required  bool            `json:"required"`
		Type      string          `json:"type"`
		Options   json.RawMessage `json:"options"`
		SortOrder int             `json:"sortOrder"`
	} `json:"fields"`
	DocumentCategories []struct {
		Name      string `json:"name"`
		Label     string `json:"label"`
		Required  bool   `json:"required"`
		SortOrder int    `json:"sortOrder"`
	} `json:"documentCategories"`
}

func (r *Reader) personioURL(host, jobID string) string {
	if r.personioBase != "" {
		return r.personioBase + "/job/" + url.PathEscape(jobID) + "/apply?language=en"
	}
	return "https://" + host + "/job/" + url.PathEscape(jobID) + "/apply?language=en"
}

func (r *Reader) personio(ctx context.Context, host, company, jobID, applyURL string) (Form, error) {
	known := Form{Provider: ProviderPersonio, Board: company, JobID: jobID, ApplyURL: applyURL}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.personioURL(host, jobID), nil)
	if err != nil {
		return known, err
	}
	response, err := r.client.Do(request)
	if err != nil {
		return known, fmt.Errorf("could not reach the employer's job board: %w", err)
	}
	defer response.Body.Close()
	switch {
	case r.companyGone(response.Request.URL):
		// A company Personio no longer hosts is sent to Personio's own home
		// page, whatever that page then answers: none of its postings exist.
		return known, ErrNotFound
	case response.StatusCode == http.StatusNotFound:
		return known, ErrNotFound
	case response.StatusCode != http.StatusOK:
		return known, fmt.Errorf("the employer's job board returned %d", response.StatusCode)
	}
	page, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return known, err
	}
	form, ok := personioFormIn(page)
	if !ok {
		return known, errPersonioUnusable
	}
	return convertPersonio(form, known), nil
}

// companyGone tells whether a request for a career site ended on Personio's
// home page, which is where Personio sends a company it no longer hosts. Only
// that page counts: any other detour leaves the form unread, never the job
// closed. Tests stand any host but the stub's in for it.
func (r *Reader) companyGone(final *url.URL) bool {
	if r.personioBase != "" {
		return !strings.HasPrefix(final.String(), r.personioBase)
	}
	return personioHomepage(final)
}

var personioHome = regexp.MustCompile(`^(?:/[a-z]{2}(?:-[a-z]{2})?)?/?$`)

func personioHomepage(page *url.URL) bool {
	switch strings.ToLower(page.Hostname()) {
	case "personio.com", "www.personio.com", "personio.de", "www.personio.de":
		return personioHome.MatchString(page.Path)
	}
	return false
}

// personioFormIn finds the form in the page's streamed data: string chunks
// pushed to self.__next_f that together hold `"form":{"fields":[...]}`.
func personioFormIn(page []byte) (personioForm, bool) {
	var payload strings.Builder
	for _, match := range personioFlightChunk.FindAllSubmatch(page, -1) {
		var chunk string
		if json.Unmarshal(append(append([]byte(`"`), match[1]...), '"'), &chunk) == nil {
			payload.WriteString(chunk)
		}
	}
	data := payload.String()
	at := strings.Index(data, `"form":{"fields":`)
	if at < 0 {
		return personioForm{}, false
	}
	var form personioForm
	// The decoder reads the one object and ignores what follows it.
	if err := json.NewDecoder(strings.NewReader(data[at+len(`"form":`):])).Decode(&form); err != nil || len(form.Fields) == 0 {
		return personioForm{}, false
	}
	return form, true
}

func convertPersonio(source personioForm, form Form) Form {
	fields := source.Fields
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].SortOrder < fields[j].SortOrder })
	for _, item := range fields {
		label := strings.TrimSpace(item.Label)
		if standard, ok := personioLabels[item.Name]; ok {
			label = standard
		}
		if item.Name == "" || label == "" {
			continue
		}
		field := Field{Name: item.Name, Label: label, Type: TypeText, Role: RoleQuestion, Required: item.Required}
		switch item.Type {
		case "option":
			field.Type = TypeSelect
		case "multi_option", "multiple_option":
			field.Type = TypeMultiSelect
		case "textarea", "long_text":
			field.Type = TypeTextarea
		}
		field.Options = personioOptions(item.Options)
		form.admit(field)
	}
	documents := source.DocumentCategories
	sort.SliceStable(documents, func(i, j int) bool { return documents[i].SortOrder < documents[j].SortOrder })
	for _, document := range documents {
		label := strings.TrimSpace(document.Label)
		if label == "" {
			label = document.Name
		}
		field := Field{Name: "document:" + document.Name, Label: label, Type: TypeFile, Role: RoleQuestion, Required: document.Required}
		switch {
		case document.Name == "cv" || resumePattern.MatchString(label):
			field.Role = RoleResume
		case document.Name == "cover_letter" || coverLetterPattern.MatchString(label):
			field.Role = RoleCoverLetter
		}
		form.admit(field)
	}
	return form
}

// personioOptions reads a field's options in the order the page lists them.
// They arrive as an object of id to label, which a Go map would reorder, so
// the object is read token by token; a plain list is read as it stands.
func personioOptions(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == 'n' {
		return nil
	}
	var out []string
	if raw[0] == '[' {
		var list []any
		if json.Unmarshal(raw, &list) == nil {
			for _, item := range list {
				if text := stringify(item); text != "" {
					out = append(out, text)
				}
			}
		}
		return out
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	for decoder.More() {
		if _, err := decoder.Token(); err != nil { // the option's id
			return out
		}
		var label any
		if err := decoder.Decode(&label); err != nil {
			return out
		}
		if text := stringify(label); text != "" {
			out = append(out, text)
		}
	}
	return out
}
