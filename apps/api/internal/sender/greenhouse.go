package sender

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"jobpilot/core/api/internal/models"
)

// HandBack is a send stopped before anything reached the employer: the form
// needs a person. Its text is for the user, and says what to do.
type HandBack string

func (h HandBack) Error() string { return string(h) }

// Unknown is a send that may have reached the employer without an answer
// saying so. It is never tried again.
type Unknown string

func (u Unknown) Error() string { return string(u) }

// File is a document attached to a form.
type File struct {
	Name        string
	ContentType string
	Data        []byte
}

// Submission is what an approved entry sends: the answers it carries, under the
// field names the employer's form gave when the entry was composed.
type Submission struct {
	Board       string
	JobID       string
	Answers     []models.OutboxAnswer
	CoverLetter string
	CV          *File
	// BeforePost runs once everything has been checked, just before the form
	// is posted, so the attempt is on record before it can reach the employer.
	// The post does not happen if it fails.
	BeforePost func() error
}

// userAgent says what is posting. The sender never presents itself as a browser.
const userAgent = "JobPilot (self-hosted application assistant; sends only what its user approved)"

// greenhouseForms is where Greenhouse hosts each job's application form.
const greenhouseForms = "https://job-boards.greenhouse.io"

// Greenhouse posts approved entries to a job's form on Greenhouse's hosted
// job board.
type Greenhouse struct {
	origin string
	client *http.Client
}

// NewGreenhouse sends to Greenhouse's hosted forms. A base other than the
// public board API (ATS_GREENHOUSE_URL pointed at a fake) serves the form pages
// too, so a test reads and posts forms where it serves the board API.
func NewGreenhouse(base string) *Greenhouse {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	origin := base
	if base == "" || base == "https://boards-api.greenhouse.io" {
		origin = greenhouseForms
	}
	return &Greenhouse{origin: origin, client: &http.Client{
		Timeout: time.Minute,
		// A redirect is answered, never followed: the form page moving to
		// another site, or the post being answered with a redirect, is read
		// as such.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Submit reads the job's form page and posts the entry to it. It returns nil
// when the employer accepted the post, a HandBack when nothing was posted, and
// an Unknown when the post went out without a clear answer.
func (g *Greenhouse) Submit(ctx context.Context, sub Submission) error {
	if sub.Board == "" || sub.JobID == "" {
		return HandBack("This application does not say which Greenhouse job it is for. Prepare it again, or apply on the employer's site.")
	}
	page, err := url.Parse(fmt.Sprintf("%s/%s/jobs/%s", g.origin, url.PathEscape(sub.Board), url.PathEscape(sub.JobID)))
	if err != nil {
		return HandBack("The employer's form address could not be built, so nothing was sent. Apply on the employer's site.")
	}
	body, err := g.fetch(ctx, page)
	if err != nil {
		return err
	}
	if guarded(body) {
		return HandBack("The employer's form is protected by a CAPTCHA, which JobPilot never solves, so nothing was sent. Open the form and send the application yourself.")
	}
	forms, err := parseForms(bytes.NewReader(body))
	if err != nil {
		return HandBack("The employer's form page could not be read, so nothing was sent. Apply on the employer's site.")
	}
	form, err := pick(forms, sub.Answers)
	if err != nil {
		return err
	}
	action, err := page.Parse(form.action)
	if err != nil || action.Scheme != page.Scheme || action.Host != page.Host {
		return HandBack("The employer's form sends applications to another site, so JobPilot did not send it. Apply on the employer's site.")
	}
	payload, contentType, err := fill(form, sub)
	if err != nil {
		return err
	}
	if sub.BeforePost != nil {
		if err := sub.BeforePost(); err != nil {
			return HandBack("Sending was stopped before anything reached the employer: " + err.Error())
		}
	}
	return g.post(ctx, page, action, payload, contentType, form)
}

func (g *Greenhouse) fetch(ctx context.Context, page *url.URL) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return nil, HandBack("The employer's form could not be opened, so nothing was sent. Apply on the employer's site.")
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "text/html")
	response, err := g.client.Do(request)
	if err != nil {
		return nil, HandBack("The employer's form could not be reached, so nothing was sent. Apply on the employer's site, or prepare the application again later.")
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return nil, HandBack("The employer's form is gone: the posting may have closed. Nothing was sent.")
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return nil, HandBack("The employer's form has moved to their own site, so nothing was sent. Apply there.")
	case response.StatusCode != http.StatusOK:
		return nil, HandBack(fmt.Sprintf("The employer's form answered %d, so nothing was sent. Apply on the employer's site.", response.StatusCode))
	}
	// One byte past the limit tells a page that ends from one that was cut
	// short, whose unread part could hold a CAPTCHA.
	body, err := io.ReadAll(io.LimitReader(response.Body, pageLimit+1))
	if err != nil {
		return nil, HandBack("The employer's form could not be read, so nothing was sent. Apply on the employer's site.")
	}
	if len(body) > pageLimit {
		return nil, HandBack("The employer's form page is too large to read in full, so nothing was sent. Apply on the employer's site.")
	}
	return body, nil
}

// pageLimit bounds a form page or an answer the sender reads.
const pageLimit = 4 << 20

// guarded reports a page that loads a CAPTCHA or a bot challenge: reCAPTCHA,
// hCaptcha, Cloudflare Turnstile, Arkose and the like all name themselves in
// the page that loads them.
func guarded(page []byte) bool {
	lower := bytes.ToLower(page)
	for _, marker := range []string{"captcha", "turnstile", "challenges.cloudflare.com", "arkoselabs"} {
		if bytes.Contains(lower, []byte(marker)) {
			return true
		}
	}
	return false
}

// sendsValue reports an answer the sender fills in. Declarations, personal
// choices and other uploads are the applicant's own and are left; compose has
// already held back any of them that is required. An optional answer a model
// or the template drafted is left too: a draft is never sent before the user
// approves it, and approval only checks the required ones in the answer bank.
func sendsValue(answer models.OutboxAnswer) bool {
	switch answer.Source {
	case "declaration", "personal", "upload":
		return false
	case "ai", "template":
		if answer.NeedsReview && !answer.Required {
			return false
		}
	case "cv", "cover_letter":
		return true
	}
	return strings.TrimSpace(answer.Answer) != ""
}

// pick chooses the form that carries every field the entry fills.
func pick(forms []webForm, answers []models.OutboxAnswer) (webForm, error) {
	var posts []webForm
	for _, form := range forms {
		if form.post {
			posts = append(posts, form)
		}
	}
	if len(posts) == 0 {
		return webForm{}, HandBack("The employer's form is not a plain web form JobPilot can fill, so nothing was sent. Open the form and send the application yourself.")
	}
	var missing string
	for i, form := range posts {
		absent := ""
		for _, answer := range answers {
			if _, ok := form.inputs[answer.Field]; !ok && sendsValue(answer) {
				absent = answer.Label
				break
			}
		}
		if absent == "" {
			return form, nil
		}
		if i == 0 {
			missing = absent
		}
	}
	return webForm{}, HandBack(fmt.Sprintf("The employer's form no longer has the question %q, so nothing was sent. Prepare the application again, or apply on the employer's site.", missing))
}

// fill builds the post: hidden values as the page set them, then every answer
// the entry carries. Anything it cannot place exactly is handed back.
func fill(form webForm, sub Submission) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	filled := map[string]bool{}
	for _, name := range form.order {
		if in := form.inputs[name]; in.kind == kindHidden {
			if err := writer.WriteField(name, in.value); err != nil {
				return nil, "", err
			}
			filled[name] = true
		}
	}
	for _, answer := range sub.Answers {
		if !sendsValue(answer) {
			continue
		}
		in := form.inputs[answer.Field]
		switch answer.Source {
		case "cv":
			if sub.CV == nil {
				return nil, "", HandBack("The approved CV could not be attached, so nothing was sent. Prepare the application again.")
			}
			if in.kind != kindFile {
				return nil, "", HandBack(fmt.Sprintf("The employer's form does not take the CV as a file at %q, so nothing was sent. Apply on the employer's site.", answer.Label))
			}
			if err := attach(writer, answer.Field, *sub.CV); err != nil {
				return nil, "", err
			}
		case "cover_letter":
			letter := strings.TrimSpace(sub.CoverLetter)
			if letter == "" {
				continue
			}
			var err error
			switch in.kind {
			case kindFile:
				err = attach(writer, answer.Field, File{Name: "cover-letter.txt", ContentType: "text/plain; charset=utf-8", Data: []byte(letter)})
			case kindText, kindTextarea:
				err = writer.WriteField(answer.Field, letter)
			default:
				return nil, "", HandBack(fmt.Sprintf("JobPilot cannot place the cover letter in %q, so nothing was sent. Apply on the employer's site.", answer.Label))
			}
			if err != nil {
				return nil, "", err
			}
		default:
			value, ok := answer.Answer, true
			switch in.kind {
			case kindText, kindTextarea:
			case kindSelect, kindRadio, kindCheckbox:
				value, ok = in.choose(answer.Answer)
			default:
				ok = false
			}
			if !ok {
				return nil, "", HandBack(fmt.Sprintf("The employer's form does not offer your answer to %q as a choice, so nothing was sent. Prepare the application again, or apply on the employer's site.", answer.Label))
			}
			if err := writer.WriteField(answer.Field, value); err != nil {
				return nil, "", err
			}
		}
		filled[answer.Field] = true
	}
	for _, name := range form.order {
		if form.inputs[name].required && !filled[name] {
			return nil, "", HandBack(fmt.Sprintf("The employer's form asks for %q, which this application does not carry, so nothing was sent. Apply on the employer's site.", name))
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func attach(writer *multipart.Writer, field string, file File) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quote(field), quote(file.Name)))
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(file.Data)
	return err
}

// quote escapes a header parameter as mime/multipart does for its own fields.
func quote(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "", "\n", "").Replace(s)
}

// post sends the application. Only an answer the employer clearly gave counts:
// a success page or a redirect onward is sent, a refusal or the form shown
// again is handed back, and anything else once the request has left, a
// challenge or an answer that cannot be read included, is unknown: it may
// have arrived, so it is never sent again, but it is not called sent.
func (g *Greenhouse) post(ctx context.Context, page, action *url.URL, payload []byte, contentType string, form webForm) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, action.String(), bytes.NewReader(payload))
	if err != nil {
		return HandBack("The application could not be prepared for sending, so nothing was sent.")
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Referer", page.String())
	response, err := g.client.Do(request)
	if err != nil {
		return Unknown("The employer's form did not answer while the application was being sent. Check with them before applying again; JobPilot will not send it a second time.")
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		// Onward to another page is the usual confirmation; no destination,
		// or back to the form, confirms nothing.
		next, err := action.Parse(response.Header.Get("Location"))
		if response.Header.Get("Location") == "" || err != nil || sameDocument(next, page) || sameDocument(next, action) {
			return Unknown(unconfirmed)
		}
		return nil
	case response.StatusCode >= 200 && response.StatusCode < 300:
		body, err := io.ReadAll(io.LimitReader(response.Body, pageLimit+1))
		if err != nil || len(body) > pageLimit || guarded(body) {
			return Unknown(unconfirmed)
		}
		if cameBack(body, form) {
			return HandBack("The employer's form came back without confirming the application, so it was most likely not received. Open the form and send it yourself.")
		}
		return nil
	case response.StatusCode >= 400 && response.StatusCode < 500:
		return HandBack(fmt.Sprintf("The employer's form refused the application (%d), so it was not received. Open the form and send it yourself.", response.StatusCode))
	default:
		return Unknown(fmt.Sprintf("The employer's form failed (%d) while receiving the application. Check with them before applying again; JobPilot will not send it a second time.", response.StatusCode))
	}
}

// unconfirmed is an answer to the post that neither confirms nor refuses it.
const unconfirmed = "The employer's form did not confirm the application, so it may or may not have arrived. Check with them before applying again; JobPilot will not send it a second time."

// sameDocument reports two addresses of one page, whatever their query.
func sameDocument(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && a.Host == b.Host && strings.TrimSuffix(a.Path, "/") == strings.TrimSuffix(b.Path, "/")
}

// cameBack reports an answer that is the same form again, which is how a form
// shows its own validation errors instead of a confirmation.
func cameBack(body []byte, sent webForm) bool {
	forms, err := parseForms(bytes.NewReader(body))
	if err != nil {
		return false
	}
	for _, form := range forms {
		if !form.post || len(sent.order) == 0 {
			continue
		}
		same := true
		for _, name := range sent.order {
			if _, ok := form.inputs[name]; !ok {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}
