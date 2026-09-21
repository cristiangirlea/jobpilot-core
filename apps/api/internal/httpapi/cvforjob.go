package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/answers"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/cv"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// Decisions returned by cvForJob. The caller branches on these, and so does the
// review UI: only "generated" needs the user to do anything.
const (
	decisionBase      = "base"      // the approved base CV already speaks to this role
	decisionReused    = "reused"    // an approved tailored CV covers this kind of role
	decisionGenerated = "generated" // a new tailored CV was made and needs approval
)

// tailoredReuseFloor is how similar a posting's stack must be to the one a
// tailored CV was approved for before that CV is reused. It is stricter than
// the answer bank's floor: a wrong sentence in an answer is awkward, a CV
// emphasising the wrong stack undermines the whole application.
const tailoredReuseFloor = 0.6

type cvForJobResponse struct {
	Decision  string            `json:"decision"`
	Reason    string            `json:"reason"`
	Alignment float64           `json:"alignment"`
	CV        models.CVDocument `json:"cv"`
	Preview   *cv.Tailored      `json:"preview,omitempty"`
	Approved  bool              `json:"approved"`
	Highlight []string          `json:"highlighted,omitempty"`
	Missing   []string          `json:"missing,omitempty"`
}

// cvForJob decides which CV this application should carry.
//
// The order implements the rule the product promises: send the approved base CV
// when it already fits, otherwise reuse a tailored CV already approved for this
// kind of role, and only then build a new one - which waits for approval rather
// than being sent.
// cvDecision is the outcome of choosing a CV for one job.
type cvDecision struct {
	Decision  string
	Reason    string
	Alignment float64
	CV        models.CVDocument
	Preview   *cv.Tailored
	Approved  bool
}

// decideCV chooses which CV an application should carry, generating one when
// nothing suitable is approved yet.
//
// The order implements the rule the product promises: send the approved base CV
// when it already fits, otherwise reuse a tailored CV already approved for this
// kind of role, and only then build a new one - which waits for approval rather
// than being sent.
func (s *Server) decideCV(ctx context.Context, uid string, job models.JobWithMatch, profile models.Profile) (cvDecision, error) {
	base, baseErr := s.Store.ApprovedBaseCV(ctx, uid)
	if baseErr != nil && !errors.Is(baseErr, pgx.ErrNoRows) {
		return cvDecision{}, baseErr
	}
	hasBase := baseErr == nil

	baseText := ""
	if hasBase {
		if full, textErr := s.Store.CV(ctx, uid, base.ID); textErr == nil {
			baseText = full.ExtractedText
		}
	}
	alignment := cv.Alignment(profile, baseText, job.Job)

	// 1. The base CV already speaks to this posting.
	if hasBase && alignment >= cv.AlignmentThreshold {
		return cvDecision{
			Decision:  decisionBase,
			Reason:    fmt.Sprintf("Your approved CV already covers %.0f%% of this posting's technologies.", alignment*100),
			Alignment: alignment, CV: base, Approved: true,
		}, nil
	}

	family := answers.RoleFamily(job.Job.Title)
	posting := answers.TechnologySignature(20, job.Job.Title, job.Job.Description)

	// 2. A tailored CV approved for a similar role covers this one too.
	candidates, err := s.Store.ApprovedTailoredCVs(ctx, uid, family, 20)
	if err != nil {
		return cvDecision{}, err
	}
	if best, overlap, ok := bestTailored(candidates, family, posting); ok {
		return cvDecision{
			Decision:  decisionReused,
			Reason:    fmt.Sprintf("Reusing a CV you already approved for %s roles (%.0f%% stack overlap).", best.RoleFamily, overlap*100),
			Alignment: alignment, CV: best, Approved: true,
		}, nil
	}

	// 3. Build one. It is stored unapproved, so it cannot be sent yet.
	tailored := cv.Tailor(ctx, s.LLM, profile, job.Job, job.Match, baseText)
	data, err := cv.RenderPDF(tailored)
	if err != nil {
		return cvDecision{}, err
	}
	sum := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(sum[:])

	// An identical file may already be approved from an earlier job; reuse that
	// approval rather than resetting it by saving over the row.
	if existing, found, lookupErr := s.Store.CVByFingerprint(ctx, uid, fingerprint); lookupErr == nil && found && existing.Approved {
		return cvDecision{
			Decision:  decisionReused,
			Reason:    "This posting produces a CV identical to one you already approved.",
			Alignment: alignment, CV: existing, Approved: true,
		}, nil
	}

	document := models.CVDocument{
		Kind:         "tailored",
		Label:        strings.TrimSpace(job.Job.Title + " — " + job.Job.Company),
		Filename:     tailoredFilename(profile.Name, job.Job.Title),
		Format:       cv.FormatPDF,
		ContentType:  "application/pdf",
		SizeBytes:    len(data),
		Parsed:       toMap(cv.Parsed{}),
		RoleFamily:   family,
		Technologies: posting,
		Fingerprint:  fingerprint,
	}
	if hasBase {
		document.ParentID = base.ID
	}
	// The rendered text is stored so the user can read what will be sent without
	// opening the file.
	if text, _, textErr := cv.ExtractText("tailored.pdf", data); textErr == nil {
		document.ExtractedText = text
	}

	saved, err := s.Store.SaveCV(ctx, uid, document, data)
	if err != nil {
		return cvDecision{}, err
	}
	reason := fmt.Sprintf("Your CV covers only %.0f%% of this posting's technologies, so a tailored version was prepared. Approve it to send it, and it will be reused for similar roles.", alignment*100)
	if !hasBase {
		reason = "No approved base CV was found, so a tailored version was prepared from your profile. Approve it to send it."
	}
	return cvDecision{
		Decision: decisionGenerated, Reason: reason, Alignment: alignment,
		CV: saved, Preview: &tailored, Approved: false,
	}, nil
}

func (s *Server) cvForJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := auth.UserID(ctx)

	job, err := s.Store.Job(ctx, uid, r.PathValue("id"))
	if err != nil {
		respond(w, job, err)
		return
	}
	if job.Match.Rejected {
		problem(w, 409, store.ErrApplicationBlocked.Error())
		return
	}
	profile, err := s.Store.Profile(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	decision, err := s.decideCV(ctx, uid, job, profile)
	if err != nil {
		internal(w, err)
		return
	}
	response := cvForJobResponse{
		Decision: decision.Decision, Reason: decision.Reason, Alignment: decision.Alignment,
		CV: decision.CV, Preview: decision.Preview, Approved: decision.Approved,
	}
	status := 200
	if decision.Decision == decisionGenerated {
		status = 201
		response.Highlight = decision.Preview.Highlighted
		response.Missing = decision.Preview.Missing
	}
	write(w, status, response)
}

// bestTailored picks the approved tailored CV closest to this posting, provided
// it clears the reuse floor. Same role family is required as well as stack
// overlap, because a backend CV reused for a data role reads as a mismatch even
// when the technologies coincide.
func bestTailored(candidates []models.CVDocument, family string, posting []string) (models.CVDocument, float64, bool) {
	best := models.CVDocument{}
	bestOverlap := 0.0
	found := false
	for _, candidate := range candidates {
		if candidate.RoleFamily != family {
			continue
		}
		overlap := answers.Overlap(posting, candidate.Technologies)
		if overlap >= tailoredReuseFloor && overlap > bestOverlap {
			best, bestOverlap, found = candidate, overlap, true
		}
	}
	return best, bestOverlap, found
}

// tailoredFilename gives the employer a file named after the candidate rather
// than something like "tailored.pdf".
func tailoredFilename(name, title string) string {
	clean := func(v string) string {
		v = strings.TrimSpace(v)
		var b strings.Builder
		for _, r := range v {
			// Diacritics are transliterated rather than dropped: a filename is
			// ASCII for the sake of mail clients and Content-Disposition, but
			// "Prvan" instead of "Pirvan" mangles the candidate's own name.
			if replacement, ok := asciiFold[r]; ok {
				b.WriteString(replacement)
				continue
			}
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			case r == ' ' || r == '-' || r == '_':
				b.WriteByte('-')
			}
		}
		return strings.Trim(b.String(), "-")
	}
	who := clean(name)
	if who == "" {
		who = "CV"
	}
	what := clean(title)
	if what == "" {
		return who + "-CV.pdf"
	}
	return who + "-" + what + ".pdf"
}

// asciiFold maps the Latin diacritics that appear in European names onto their
// base letters, for use where only ASCII is safe.
var asciiFold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ă': "a", 'ą': "a",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ę': "e", 'ě': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ů': "u",
	'ç': "c", 'ć': "c", 'č': "c", 'ñ': "n", 'ń': "n", 'ř': "r",
	'ś': "s", 'š': "s", 'ș': "s", 'ş': "s", 'ť': "t", 'ț': "t", 'ţ': "t",
	'ý': "y", 'ÿ': "y", 'ź': "z", 'ż': "z", 'ž': "z", 'ł': "l", 'đ': "d",
	'ß': "ss", 'æ': "ae", 'œ': "oe",
	'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A", 'Ă': "A", 'Ą': "A",
	'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E", 'Ę': "E", 'Ě': "E",
	'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I",
	'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O", 'Ø': "O",
	'Ù': "U", 'Ú': "U", 'Û': "U", 'Ü': "U", 'Ů': "U",
	'Ç': "C", 'Ć': "C", 'Č': "C", 'Ñ': "N", 'Ń': "N", 'Ř': "R",
	'Ś': "S", 'Š': "S", 'Ș': "S", 'Ş': "S", 'Ť': "T", 'Ț': "T", 'Ţ': "T",
	'Ý': "Y", 'Ź': "Z", 'Ż': "Z", 'Ž': "Z", 'Ł': "L", 'Đ': "D",
}
