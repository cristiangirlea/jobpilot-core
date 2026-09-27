package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/store"
)

// jobReport is what reporting a posting as no longer available did.
type jobReport struct {
	// Closed is true when the posting is now closed for everyone.
	Closed bool   `json:"closed"`
	Reason string `json:"reason,omitempty"`
	// HiddenForYou is true when nothing confirmed it yet: the job leaves the
	// reporter's lists and stays open for everyone else.
	HiddenForYou bool   `json:"hidden_for_you"`
	Message      string `json:"message"`
}

// reportCheckTimeout bounds each request of the link check a report runs.
const reportCheckTimeout = 10 * time.Second

// reportJob records that the user says a posting is no longer available. The
// link check catches a 404 or 410 once a day, but not a page that says the
// role is filled and still answers 200. A report checks the link at once,
// and a definite answer closes the job for everyone. Otherwise the job
// leaves the reporter's lists, and closes for everyone when a second person
// reports it: the catalog is shared, and one mistaken report must not hide a
// live job from others.
func (s *Server) reportJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := auth.UserID(ctx)
	job, err := s.Store.Job(ctx, uid, r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Job not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	if job.Job.ClosedAt != nil {
		write(w, 200, jobReport{Closed: true, Reason: job.Job.ClosedReason, Message: "This posting had already closed."})
		return
	}
	reporters, err := s.Store.ReportJob(ctx, uid, job.Job.ID)
	if err != nil {
		internal(w, err)
		return
	}
	if s.Discovery != nil {
		links, err := s.Store.JobLinks(ctx, job.Job.ID)
		if err != nil {
			internal(w, err)
			return
		}
		if result := s.Discovery.CheckJob(ctx, links, reportCheckTimeout); result.Closed {
			write(w, 200, jobReport{Closed: true, Reason: result.Reason, Message: "Its link confirms it, so the posting has closed for everyone."})
			return
		}
	}
	if reporters >= store.ReportsToClose {
		if err = s.Store.CloseJob(ctx, job.Job.ID, discovery.ReasonReported); err != nil {
			internal(w, err)
			return
		}
		write(w, 200, jobReport{Closed: true, Reason: discovery.ReasonReported, Message: "Someone else reported it too, so the posting has closed for everyone."})
		return
	}
	write(w, 200, jobReport{HiddenForYou: true, Message: "Hidden from your lists. Its link does not confirm it, so it stays open for others until the link does or someone else reports it too."})
}

// withdrawReport takes the user's report back. A job that closed on reports
// alone reopens once too few remain; one its link check closed stays closed.
func (s *Server) withdrawReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := auth.UserID(ctx)
	job, err := s.Store.Job(ctx, uid, r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Job not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	if err = s.Store.WithdrawReport(ctx, uid, job.Job.ID, discovery.ReasonReported); err != nil {
		internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
