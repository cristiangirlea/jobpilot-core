// Package jobpilot exposes the JobPilot REST API to MCP hosts as a small set
// of typed tools. It is a client of the API, not a second back end: every
// call carries the user's bearer token, so user isolation, rejection locks,
// and the no-submission boundary are enforced by the API exactly as they are
// for the web UI.
package jobpilot
