package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobpilot/core/api/internal/models"
)

type Fetcher struct {
	Client              *http.Client
	RemotiveBase        string
	GreenhouseBase      string
	LeverBase           string
	AshbyBase           string
	SmartRecruitersBase string
	ArbeitnowBase       string
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		Client:              &http.Client{Timeout: 20 * time.Second},
		RemotiveBase:        "https://remotive.com/api/remote-jobs",
		GreenhouseBase:      "https://boards-api.greenhouse.io/v1/boards",
		LeverBase:           "https://api.lever.co/v0/postings",
		AshbyBase:           "https://api.ashbyhq.com/posting-api/job-board",
		SmartRecruitersBase: "https://api.smartrecruiters.com/v1/companies",
		ArbeitnowBase:       "https://www.arbeitnow.com/api/job-board-api",
	}
}

// UseGreenhouseOrigin reads board listings from another Greenhouse origin,
// the one ATS_GREENHOUSE_URL names for the form reader, so one setting moves
// both to a test double. An empty origin keeps the public API.
func (f *Fetcher) UseGreenhouseOrigin(origin string) {
	if origin = strings.TrimSuffix(strings.TrimSpace(origin), "/"); origin != "" {
		f.GreenhouseBase = origin + "/v1/boards"
	}
}

func (f *Fetcher) Fetch(ctx context.Context, source models.DiscoverySource) ([]models.DiscoveredJob, error) {
	switch source.Provider {
	case "remotive":
		return f.remotive(ctx, source.SearchQuery)
	case "greenhouse":
		return f.greenhouse(ctx, source.Identifier, source.Name)
	case "lever":
		return f.lever(ctx, source.Identifier, source.Name)
	case "ashby":
		return f.ashby(ctx, source.Identifier, source.Name)
	case "smartrecruiters":
		return f.smartRecruiters(ctx, source.Identifier, source.Name)
	case "arbeitnow":
		return f.arbeitnow(ctx, source.SearchQuery, source.ExcludedSearchQuery)
	default:
		return nil, errors.New("unsupported discovery provider")
	}
}

func (f *Fetcher) get(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "JobPilot-Core/1.0 (+local job discovery assistant)")
	res, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return fmt.Errorf("source returned HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 10<<20)).Decode(out)
}

func (f *Fetcher) remotive(ctx context.Context, query string) ([]models.DiscoveredJob, error) {
	u, err := url.Parse(f.RemotiveBase)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if query != "" {
		q.Set("search", query)
	}
	q.Set("limit", "100")
	u.RawQuery = q.Encode()
	var payload struct {
		Jobs []struct {
			ID          int      `json:"id"`
			URL         string   `json:"url"`
			Title       string   `json:"title"`
			Company     string   `json:"company_name"`
			Category    string   `json:"category"`
			Tags        []string `json:"tags"`
			JobType     string   `json:"job_type"`
			Published   string   `json:"publication_date"`
			Location    string   `json:"candidate_required_location"`
			Salary      string   `json:"salary"`
			Description string   `json:"description"`
		} `json:"jobs"`
	}
	if err = f.get(ctx, u.String(), &payload); err != nil {
		return nil, err
	}
	out := make([]models.DiscoveredJob, 0, len(payload.Jobs))
	for _, x := range payload.Jobs {
		posted := parseTime(x.Published)
		description := Readable(x.Description)
		if x.Salary != "" {
			description += "\n\nCompensation listed by source: " + x.Salary
		}
		out = append(out, models.DiscoveredJob{ExternalID: strconv.Itoa(x.ID), Input: models.JobInput{Title: x.Title, Company: x.Company, Location: x.Location, RemoteType: "remote", EmploymentType: normalizeEmployment(x.JobType), Industry: x.Category, Description: description, ApplyURL: x.URL, PostedAt: posted}})
	}
	return out, nil
}

func (f *Fetcher) greenhouse(ctx context.Context, identifier, companyName string) ([]models.DiscoveredJob, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, errors.New("Greenhouse board token is required")
	}
	u := strings.TrimSuffix(f.GreenhouseBase, "/") + "/" + pathEscape(identifier) + "/jobs?content=true"
	var payload struct {
		Jobs []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Updated  string `json:"updated_at"`
			URL      string `json:"absolute_url"`
			Content  string `json:"content"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			Departments []struct {
				Name string `json:"name"`
			} `json:"departments"`
		} `json:"jobs"`
	}
	if err := f.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]models.DiscoveredJob, 0, len(payload.Jobs))
	for _, x := range payload.Jobs {
		industry := ""
		if len(x.Departments) > 0 {
			industry = x.Departments[0].Name
		}
		out = append(out, models.DiscoveredJob{ExternalID: strconv.FormatInt(x.ID, 10), Input: models.JobInput{Title: x.Title, Company: companyName, Location: x.Location.Name, RemoteType: remoteType(x.Location.Name), Industry: industry, Description: Readable(x.Content), ApplyURL: x.URL, PostedAt: parseTime(x.Updated)}})
	}
	return out, nil
}

func (f *Fetcher) lever(ctx context.Context, identifier, companyName string) ([]models.DiscoveredJob, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, errors.New("Lever site slug is required")
	}
	u := strings.TrimSuffix(f.LeverBase, "/") + "/" + pathEscape(identifier) + "?mode=json"
	var payload []struct {
		ID          string `json:"id"`
		Text        string `json:"text"`
		Description string `json:"descriptionPlain"`
		Additional  string `json:"additionalPlain"`
		HostedURL   string `json:"hostedUrl"`
		ApplyURL    string `json:"applyUrl"`
		Created     int64  `json:"createdAt"`
		Categories  struct {
			Commitment string `json:"commitment"`
			Location   string `json:"location"`
			Team       string `json:"team"`
		} `json:"categories"`
		Lists []struct {
			Text    string `json:"text"`
			Content string `json:"content"`
		} `json:"lists"`
	}
	if err := f.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]models.DiscoveredJob, 0, len(payload))
	for _, x := range payload {
		description := x.Description
		for _, list := range x.Lists {
			description += "\n\n" + list.Text + "\n" + Readable(list.Content)
		}
		description += "\n\n" + x.Additional
		apply := x.ApplyURL
		if apply == "" {
			apply = x.HostedURL
		}
		var posted *time.Time
		if x.Created > 0 {
			t := time.UnixMilli(x.Created).UTC()
			posted = &t
		}
		out = append(out, models.DiscoveredJob{ExternalID: x.ID, Input: models.JobInput{Title: x.Text, Company: companyName, Location: x.Categories.Location, RemoteType: remoteType(x.Categories.Location), EmploymentType: normalizeEmployment(x.Categories.Commitment), Industry: x.Categories.Team, Description: strings.TrimSpace(description), ApplyURL: apply, PostedAt: posted}})
	}
	return out, nil
}

func (f *Fetcher) ashby(ctx context.Context, identifier, companyName string) ([]models.DiscoveredJob, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, errors.New("Ashby board name is required")
	}
	u := strings.TrimSuffix(f.AshbyBase, "/") + "/" + pathEscape(identifier)
	var payload struct {
		Jobs []struct {
			ID               string `json:"id"`
			Title            string `json:"title"`
			Department       string `json:"department"`
			Team             string `json:"team"`
			EmploymentType   string `json:"employmentType"`
			Location         string `json:"location"`
			IsRemote         bool   `json:"isRemote"`
			WorkplaceType    string `json:"workplaceType"`
			PublishedAt      string `json:"publishedAt"`
			DescriptionHTML  string `json:"descriptionHtml"`
			DescriptionPlain string `json:"descriptionPlain"`
			JobURL           string `json:"jobUrl"`
			ApplyURL         string `json:"applyUrl"`
		} `json:"jobs"`
	}
	if err := f.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]models.DiscoveredJob, 0, len(payload.Jobs))
	for _, x := range payload.Jobs {
		description := strings.TrimSpace(x.DescriptionPlain)
		if description == "" {
			description = Readable(x.DescriptionHTML)
		}
		apply := x.ApplyURL
		if apply == "" {
			apply = x.JobURL
		}
		arrangement := strings.ToLower(strings.TrimSpace(x.WorkplaceType))
		if x.IsRemote {
			arrangement = "remote"
		}
		if arrangement != "remote" && arrangement != "hybrid" && arrangement != "onsite" {
			arrangement = remoteType(x.Location + " " + x.WorkplaceType)
		}
		industry := x.Team
		if industry == "" {
			industry = x.Department
		}
		out = append(out, models.DiscoveredJob{ExternalID: x.ID, Input: models.JobInput{Title: x.Title, Company: companyName, Location: x.Location, RemoteType: arrangement, EmploymentType: normalizeEmployment(x.EmploymentType), Industry: industry, Description: description, ApplyURL: apply, PostedAt: parseTime(x.PublishedAt)}})
	}
	return out, nil
}

func (f *Fetcher) smartRecruiters(ctx context.Context, identifier, companyName string) ([]models.DiscoveredJob, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, errors.New("SmartRecruiters company identifier is required")
	}
	base := strings.TrimSuffix(f.SmartRecruitersBase, "/") + "/" + pathEscape(identifier) + "/postings"
	var listing struct {
		Content []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			ReleasedDate string `json:"releasedDate"`
			Ref          string `json:"ref"`
			Location     struct {
				City, Region, Country string
				Remote                bool
			} `json:"location"`
			Department struct {
				Label string `json:"label"`
			} `json:"department"`
			Industry struct {
				Label string `json:"label"`
			} `json:"industry"`
			TypeOfEmployment struct {
				Label string `json:"label"`
			} `json:"typeOfEmployment"`
		} `json:"content"`
	}
	if err := f.get(ctx, base+"?limit=25", &listing); err != nil {
		return nil, err
	}
	out := make([]models.DiscoveredJob, 0, len(listing.Content))
	var lastDetailErr error
	for _, x := range listing.Content {
		var detail struct {
			ApplyURL string `json:"applyUrl"`
			JobAd    struct {
				Sections struct {
					JobDescription struct {
						Text string `json:"text"`
					} `json:"jobDescription"`
					Qualifications struct {
						Text string `json:"text"`
					} `json:"qualifications"`
					AdditionalInformation struct {
						Text string `json:"text"`
					} `json:"additionalInformation"`
				} `json:"sections"`
			} `json:"jobAd"`
		}
		if err := f.get(ctx, base+"/"+pathEscape(x.ID), &detail); err != nil {
			lastDetailErr = err
			continue
		}
		description := strings.TrimSpace(strings.Join([]string{Readable(detail.JobAd.Sections.JobDescription.Text), Readable(detail.JobAd.Sections.Qualifications.Text), Readable(detail.JobAd.Sections.AdditionalInformation.Text)}, "\n\n"))
		locationParts := make([]string, 0, 3)
		for _, part := range []string{x.Location.City, x.Location.Region, x.Location.Country} {
			if strings.TrimSpace(part) != "" {
				locationParts = append(locationParts, strings.TrimSpace(part))
			}
		}
		location := strings.Join(locationParts, ", ")
		arrangement := remoteType(location)
		if x.Location.Remote {
			arrangement = "remote"
		}
		industry := x.Industry.Label
		if industry == "" {
			industry = x.Department.Label
		}
		apply := detail.ApplyURL
		if apply == "" {
			apply = "https://jobs.smartrecruiters.com/" + pathEscape(identifier) + "/" + pathEscape(x.ID)
		}
		out = append(out, models.DiscoveredJob{ExternalID: x.ID, Input: models.JobInput{Title: x.Name, Company: companyName, Location: location, RemoteType: arrangement, EmploymentType: normalizeEmployment(x.TypeOfEmployment.Label), Industry: industry, Description: description, ApplyURL: apply, PostedAt: parseTime(x.ReleasedDate)}})
	}
	if len(listing.Content) > 0 && len(out) == 0 && lastDetailErr != nil {
		return nil, fmt.Errorf("could not read SmartRecruiters job details: %w", lastDetailErr)
	}
	return out, nil
}

func (f *Fetcher) arbeitnow(ctx context.Context, query, excludedQuery string) ([]models.DiscoveredJob, error) {
	var payload struct {
		Data []struct {
			Slug        string   `json:"slug"`
			CompanyName string   `json:"company_name"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Remote      bool     `json:"remote"`
			URL         string   `json:"url"`
			Tags        []string `json:"tags"`
			JobTypes    []string `json:"job_types"`
			Location    string   `json:"location"`
			CreatedAt   int64    `json:"created_at"`
		} `json:"data"`
	}
	if err := f.get(ctx, f.ArbeitnowBase, &payload); err != nil {
		return nil, err
	}
	terms := splitOrTerms(query)
	excludedTerms := splitOrTerms(excludedQuery)
	out := make([]models.DiscoveredJob, 0, len(payload.Data))
	for _, x := range payload.Data {
		description := Readable(x.Description)
		searchText := strings.ToLower(x.Title + " " + description + " " + strings.Join(x.Tags, " "))
		matched := len(terms) == 0
		for _, term := range terms {
			if strings.Contains(searchText, term) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		excluded := false
		for _, term := range excludedTerms {
			if strings.Contains(searchText, term) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		arrangement := ""
		if x.Remote {
			arrangement = "remote"
		}
		employment := ""
		if len(x.JobTypes) > 0 {
			employment = normalizeEmployment(x.JobTypes[0])
		}
		var posted *time.Time
		if x.CreatedAt > 0 {
			t := time.Unix(x.CreatedAt, 0).UTC()
			posted = &t
		}
		out = append(out, models.DiscoveredJob{ExternalID: x.Slug, Input: models.JobInput{Title: x.Title, Company: x.CompanyName, Location: x.Location, RemoteType: arrangement, EmploymentType: employment, Description: description, ApplyURL: x.URL, PostedAt: posted}})
	}
	return out, nil
}

var orSeparator = regexp.MustCompile(`(?i)\s*(?:,|\||\bor\b)\s*`)

func splitOrTerms(value string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, term := range orSeparator.Split(strings.TrimSpace(value), -1) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
	}
	return out
}

func parseTime(v string) *time.Time {
	if v == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02"} {
		if t, e := time.Parse(layout, v); e == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}
func remoteType(v string) string {
	x := strings.ToLower(v)
	if strings.Contains(x, "hybrid") {
		return "hybrid"
	}
	if strings.Contains(x, "remote") {
		return "remote"
	}
	return ""
}
func normalizeEmployment(v string) string {
	x := strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.Contains(x, "full"):
		return "full-time"
	case strings.Contains(x, "part"):
		return "part-time"
	case strings.Contains(x, "contract"):
		return "contract"
	case strings.Contains(x, "intern"):
		return "internship"
	}
	return x
}
func pathEscape(v string) string { return url.PathEscape(strings.Trim(v, "/ ")) }
