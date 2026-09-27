package ats

import "regexp"

// Questions this software will not answer for a person.
//
// Two separate reasons, kept apart deliberately:
//
//   - Protected characteristics are voluntary, are nobody's business but the
//     applicant's, and answering them on someone's behalf is not a decision
//     software gets to make. These are dropped from the form entirely.
//   - Declarations are undertakings the applicant makes personally - that the
//     writing is their own, that they accept a policy. Ticking one for them
//     would be making a statement in their name, which is a particularly poor
//     thing for a tool that can draft text to do. These stay on the form,
//     marked so the composer leaves them for the user.
//
// Both are matched on the label, not on where the question sits. Greenhouse has
// dedicated compliance and demographic blocks, but employers are free to put the
// same questions in the ordinary list instead, and some do: Canonical asks for
// gender, nationality and race as regular required fields. Trusting the block
// alone would let exactly these questions become answerable.

var protectedPattern = regexp.MustCompile(`(?i)(\b(gender|race|ethnic|ethnicity|veteran|military|pronouns|transgender|nationality|religion|religious|marital|pregnant|pregnancy|caste)\b|disabilit|sexual orientation|sex you identify|national origin|date of birth|age range|\byour age\b)`)

var declarationPattern = regexp.MustCompile(`(?i)(\bi (agree|confirm|certify|declare|consent|understand)\b|\bconsent\b|\b(certify|declare|attest|acknowledge|signature|truthful)\b|own words|own work|written by ai|read and (agree|accept)|terms and conditions|privacy (policy|notice)|code of conduct)`)
