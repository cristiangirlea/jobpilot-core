package ats

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Recruitee serves every careers site, on recruitee.com or on an employer's
// own domain, with a public JSON API beside its pages: /api/offers/<slug>
// describes one offer and its application form. A posting links to
// /o/<slug>, sometimes behind a language prefix or with /c/new for the form.
var (
	recruiteePattern     = regexp.MustCompile(`(?i)^([a-z0-9-]+)\.recruitee\.com$`)
	recruiteeOfferPath   = regexp.MustCompile(`^(?:/[a-z]{2})?/o/([^/]+?)(?:/c/new)?/?$`)
	errRecruiteeUnusable = errors.New("Recruitee did not return this form, so it has to be completed on the employer's page")
)

type recruiteeOffer struct {
	ID                        int64  `json:"id"`
	Slug                      string `json:"slug"`
	Title                     string `json:"title"`
	CompanyName               string `json:"company_name"`
	OptionsCV                 string `json:"options_cv"`
	OptionsCoverLetter        string `json:"options_cover_letter"`
	OptionsPhone              string `json:"options_phone"`
	OptionsPhoto              string `json:"options_photo"`
	OptionsSalutation         string `json:"options_salutation"`
	OptionsTitle              string `json:"options_title"`
	LocationQuestionVisible   bool   `json:"location_question_visible"`
	LocationsQuestion         string `json:"locations_question"`
	LocationsQuestionRequired bool   `json:"locations_question_required"`
	LocationsQuestionType     string `json:"locations_question_type"`
	Locations                 []struct {
		Name string `json:"name"`
		City string `json:"city"`
	} `json:"locations"`
	OpenQuestions []struct {
		ID       int64  `json:"id"`
		Kind     string `json:"kind"`
		Required bool   `json:"required"`
		Body     string `json:"body"`
		Position int    `json:"position"`
		Options  []struct {
			Body     string `json:"body"`
			Position int    `json:"position"`
		} `json:"open_question_options"`
	} `json:"open_questions"`
}

// recruiteeOfferAt reports the offer a link names on any host. Only links on
// recruitee.com are known to be Recruitee; elsewhere the path is a guess the
// API has to confirm.
func recruiteeOfferAt(applyURL string) (host, slug string, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(applyURL))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", false
	}
	match := recruiteeOfferPath.FindStringSubmatch(parsed.Path)
	if match == nil {
		return "", "", false
	}
	return parsed.Host, match[1], true
}

func (r *Reader) recruiteeEndpoint(scheme, host, slug string) string {
	if r.recruiteeBase != "" {
		return r.recruiteeBase + "/api/offers/" + url.PathEscape(slug)
	}
	return scheme + "://" + host + "/api/offers/" + url.PathEscape(slug)
}

// recruitee reads an offer's form. onRecruitee says the host is recruitee.com
// itself, where a 404 means the offer is gone. On an employer's own domain a
// 404 may only mean the site is not Recruitee at all, so it is believed only
// when the same host's offer list shows the site is Recruitee.
func (r *Reader) recruitee(ctx context.Context, applyURL string, onRecruitee bool) (Form, error) {
	parsed, _ := url.Parse(strings.TrimSpace(applyURL))
	host, slug, _ := recruiteeOfferAt(applyURL)
	client := r.client
	if !onRecruitee {
		client = r.publicClient()
	}
	var payload struct {
		Offer recruiteeOffer `json:"offer"`
	}
	err := r.getWith(ctx, client, r.recruiteeEndpoint(parsed.Scheme, host, slug), &payload)
	switch {
	case errors.Is(err, ErrNotFound) && !onRecruitee:
		if r.isRecruiteeSite(ctx, client, parsed.Scheme, host) {
			return Form{Provider: ProviderRecruitee, Board: host, JobID: slug, ApplyURL: applyURL}, ErrNotFound
		}
		return Form{}, ErrUnsupported
	case err != nil && !onRecruitee:
		// Not JSON, refused, unreachable: nothing says this host is Recruitee.
		return Form{}, ErrUnsupported
	case err != nil:
		return Form{Provider: ProviderRecruitee, Board: host, JobID: slug, ApplyURL: applyURL}, err
	}
	offer := payload.Offer
	if offer.ID == 0 || offer.Slug == "" {
		if !onRecruitee {
			return Form{}, ErrUnsupported
		}
		return Form{Provider: ProviderRecruitee, Board: host, JobID: slug, ApplyURL: applyURL}, errRecruiteeUnusable
	}
	return convertRecruitee(offer, host, applyURL), nil
}

// isRecruiteeSite tells whether a host serves Recruitee's offer list.
func (r *Reader) isRecruiteeSite(ctx context.Context, client *http.Client, scheme, host string) bool {
	endpoint := scheme + "://" + host + "/api/offers/"
	if r.recruiteeBase != "" {
		endpoint = r.recruiteeBase + "/api/offers/"
	}
	var list struct {
		Offers *[]struct {
			ID int64 `json:"id"`
		} `json:"offers"`
	}
	return r.getWith(ctx, client, endpoint, &list) == nil && list.Offers != nil
}

// convertRecruitee lays the offer out as the form an applicant sees: the
// standard fields the employer switched on, the location question when it is
// shown, then the employer's own questions in their order.
func convertRecruitee(offer recruiteeOffer, host, applyURL string) Form {
	form := Form{Provider: ProviderRecruitee, Board: host, JobID: offer.Slug,
		Title: offer.Title, Company: offer.CompanyName, ApplyURL: applyURL}
	form.admit(Field{Name: "name", Label: "Full name", Type: TypeText, Role: RoleQuestion, Required: true})
	form.admit(Field{Name: "email", Label: "Email", Type: TypeText, Role: RoleQuestion, Required: true})
	standard := []struct {
		setting string
		field   Field
	}{
		{offer.OptionsPhone, Field{Name: "phone", Label: "Phone", Type: TypeText, Role: RoleQuestion}},
		{offer.OptionsCV, Field{Name: "cv", Label: "CV", Type: TypeFile, Role: RoleResume}},
		{offer.OptionsCoverLetter, Field{Name: "cover_letter", Label: "Cover letter", Type: TypeFile, Role: RoleCoverLetter}},
		{offer.OptionsPhoto, Field{Name: "photo", Label: "Photo", Type: TypeFile, Role: RoleQuestion}},
		{offer.OptionsTitle, Field{Name: "title", Label: "Academic title", Type: TypeText, Role: RoleQuestion}},
	}
	for _, item := range standard {
		if item.setting == "required" || item.setting == "optional" {
			item.field.Required = item.setting == "required"
			form.admit(item.field)
		}
	}
	// A salutation (Mr, Ms) states the applicant's gender. An optional one is
	// skipped like any protected question; a required one stays on the form
	// for the applicant to answer themselves, never answered for them.
	switch offer.OptionsSalutation {
	case "optional":
		form.Skipped = append(form.Skipped, "Salutation")
	case "required":
		form.Fields = append(form.Fields, Field{Name: "salutation", Label: "Salutation", Type: TypeText, Role: RoleDeclaration, Required: true})
	}
	if offer.LocationQuestionVisible && len(offer.Locations) > 0 {
		field := Field{Name: "locations", Label: strings.TrimSpace(html.UnescapeString(offer.LocationsQuestion)),
			Type: TypeSelect, Role: RoleQuestion, Required: offer.LocationsQuestionRequired}
		if field.Label == "" {
			field.Label = "Preferred location"
		}
		if offer.LocationsQuestionType == "multiple_choice" {
			field.Type = TypeMultiSelect
		}
		for _, location := range offer.Locations {
			if name := strings.TrimSpace(html.UnescapeString(firstNonEmpty(location.Name, location.City))); name != "" {
				field.Options = append(field.Options, name)
			}
		}
		form.admit(field)
	}

	questions := offer.OpenQuestions
	sort.SliceStable(questions, func(i, j int) bool { return questions[i].Position < questions[j].Position })
	for _, question := range questions {
		label := strings.TrimSpace(html.UnescapeString(question.Body))
		if label == "" {
			continue
		}
		field := Field{Name: fmt.Sprintf("question_%d", question.ID), Label: label, Type: TypeText,
			Role: RoleQuestion, Required: question.Required}
		switch question.Kind {
		case "text":
			field.Type = TypeTextarea
		case "boolean":
			field.Type, field.Options = TypeSelect, []string{"Yes", "No"}
		case "single_choice":
			field.Type = TypeSelect
		case "multi_choice", "multiple_choice":
			field.Type = TypeMultiSelect
		case "file":
			field.Type = TypeFile
		}
		options := question.Options
		sort.SliceStable(options, func(i, j int) bool { return options[i].Position < options[j].Position })
		for _, option := range options {
			if text := strings.TrimSpace(html.UnescapeString(option.Body)); text != "" {
				field.Options = append(field.Options, text)
			}
		}
		form.admit(field)
	}
	return form
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
