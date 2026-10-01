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
		Label     flightString    `json:"label"`
		Required  flightBool      `json:"required"`
		Type      flightString    `json:"type"`
		Options   json.RawMessage `json:"options"`
		SortOrder int             `json:"sortOrder"`
	} `json:"fields"`
	DocumentCategories []struct {
		Name      string       `json:"name"`
		Label     flightString `json:"label"`
		Required  flightBool   `json:"required"`
		SortOrder int          `json:"sortOrder"`
	} `json:"documentCategories"`
}

// The streamed data writes a missing value as the string "$undefined" rather
// than leaving it out. flightString reads that as empty, and flightBool reads
// anything but true as false, so one undefined flag cannot spoil the form.
type flightString string

func (s *flightString) UnmarshalJSON(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if text, ok := value.(string); ok && text != "$undefined" {
		*s = flightString(text)
	} else {
		*s = ""
	}
	return nil
}

type flightBool bool

func (b *flightBool) UnmarshalJSON(raw []byte) error {
	*b = flightBool(bytes.Equal(bytes.TrimSpace(raw), []byte("true")))
	return nil
}

func (r *Reader) personioURL(host, jobID string) string {
	return origin(r.personioBase, host) + "/job/" + url.PathEscape(jobID) + "/apply?language=en"
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

// PersonioSiteGone tells whether a link to a Personio career site ended on
// Personio's own home page, which is where Personio sends a company it no
// longer hosts. The link check uses it, since that page answers 200.
func PersonioSiteGone(link string, final *url.URL) bool {
	parsed, err := url.Parse(strings.TrimSpace(link))
	return err == nil && final != nil && personioPattern.MatchString(strings.ToLower(parsed.Hostname())) && personioHomepage(final)
}

// personioFormIn finds the application form in the page's streamed data:
// string chunks pushed to self.__next_f that together hold
// `"form":{"fields":[...]}`. A chunk that does not decode fails the whole
// read, since a hole in the data could cut the form short. When the page holds
// more than one form, the application form is the one with document slots,
// and otherwise the one with the most fields.
func personioFormIn(page []byte) (personioForm, bool) {
	var payload strings.Builder
	for _, match := range personioFlightChunk.FindAllSubmatch(page, -1) {
		var chunk string
		if err := json.Unmarshal(append(append([]byte(`"`), match[1]...), '"'), &chunk); err != nil {
			return personioForm{}, false
		}
		payload.WriteString(chunk)
	}
	data := payload.String()
	const marker = `"form":{"fields":`
	var best personioForm
	found := false
	for offset := 0; ; {
		at := strings.Index(data[offset:], marker)
		if at < 0 {
			break
		}
		start := offset + at + len(`"form":`)
		offset = start
		var form personioForm
		// The decoder reads the one object and ignores what follows it.
		if json.NewDecoder(strings.NewReader(data[start:])).Decode(&form) != nil || len(form.Fields) == 0 {
			continue
		}
		better := !found ||
			(len(form.DocumentCategories) > 0) != (len(best.DocumentCategories) > 0) && len(form.DocumentCategories) > 0 ||
			(len(form.DocumentCategories) > 0) == (len(best.DocumentCategories) > 0) && len(form.Fields) > len(best.Fields)
		if better {
			best, found = form, true
		}
	}
	return best, found
}

func convertPersonio(source personioForm, form Form) Form {
	fields := source.Fields
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].SortOrder < fields[j].SortOrder })
	for _, item := range fields {
		label := strings.TrimSpace(string(item.Label))
		if standard, ok := personioLabels[item.Name]; ok {
			label = standard
		}
		if item.Name == "" || label == "" {
			continue
		}
		field := Field{Name: item.Name, Label: label, Type: TypeText, Role: RoleQuestion, Required: bool(item.Required)}
		switch string(item.Type) {
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
		label := strings.TrimSpace(string(document.Label))
		if label == "" {
			label = document.Name
		}
		field := Field{Name: "document:" + document.Name, Label: label, Type: TypeFile, Role: RoleQuestion, Required: bool(document.Required)}
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
				if text := stringify(item); text != "" && text != "$undefined" {
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
		if text := stringify(label); text != "" && text != "$undefined" {
			out = append(out, text)
		}
	}
	return out
}
