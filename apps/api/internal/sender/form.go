// Package sender sends approved outbox entries through the employer's own
// application form, the page any applicant fills in.
//
// It reads the form page, refuses anything it may not or cannot fill, and only
// then posts. A CAPTCHA is never solved and the request never pretends to be a
// browser: a form that guards itself, that is not a plain web form, or that asks
// for something the entry does not carry goes back to the user, unsent, to
// finish on the employer's site.
package sender

import (
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Input kinds a form can hold, as the sender tells them apart.
const (
	kindText     = "text"
	kindTextarea = "textarea"
	kindFile     = "file"
	kindSelect   = "select"
	kindHidden   = "hidden"
	kindCheckbox = "checkbox"
	kindRadio    = "radio"
)

// input is one named control on a form. Radio buttons sharing a name are one
// input with an option each.
type input struct {
	kind     string
	required bool
	multiple bool
	value    string // a hidden input's value
	options  []option
}

type option struct{ value, label string }

// webForm is a <form> as the page publishes it.
type webForm struct {
	action string
	post   bool
	inputs map[string]*input
	order  []string
}

// parseForms reads every form on a page. Controls outside a form, buttons, and
// disabled controls are left out: a browser would not send them either.
func parseForms(body io.Reader) ([]webForm, error) {
	root, err := html.Parse(body)
	if err != nil {
		return nil, err
	}
	var forms []webForm
	// form is the index of the form a node sits in, or -1. An index rather
	// than a pointer, since appending may move the slice.
	var walk func(n *html.Node, form int)
	walk = func(n *html.Node, form int) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Form:
				forms = append(forms, webForm{
					action: attr(n, "action"),
					post:   strings.EqualFold(attr(n, "method"), "post"),
					inputs: map[string]*input{},
				})
				form = len(forms) - 1
			case atom.Input, atom.Textarea, atom.Select:
				if form >= 0 {
					forms[form].add(n)
				}
				if n.DataAtom == atom.Select {
					return // its options were read by add
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, form)
		}
	}
	walk(root, -1)
	return forms, nil
}

func (f *webForm) add(n *html.Node) {
	name := attr(n, "name")
	if name == "" || hasAttr(n, "disabled") {
		return
	}
	kind := kindText
	switch n.DataAtom {
	case atom.Textarea:
		kind = kindTextarea
	case atom.Select:
		kind = kindSelect
	default:
		switch strings.ToLower(attr(n, "type")) {
		case "submit", "button", "reset", "image":
			return
		case "file":
			kind = kindFile
		case "hidden":
			kind = kindHidden
		case "checkbox":
			kind = kindCheckbox
		case "radio":
			kind = kindRadio
		}
	}
	existing, seen := f.inputs[name]
	if !seen {
		existing = &input{kind: kind}
		f.inputs[name] = existing
		f.order = append(f.order, name)
	}
	existing.required = existing.required || hasAttr(n, "required")
	switch kind {
	case kindHidden:
		existing.value = attr(n, "value")
	case kindRadio, kindCheckbox:
		value := attr(n, "value")
		if !hasAttr(n, "value") {
			value = "on"
		}
		existing.options = append(existing.options, option{value: value, label: value})
	case kindSelect:
		existing.multiple = hasAttr(n, "multiple")
		existing.options = append(existing.options, selectOptions(n)...)
	}
}

func selectOptions(n *html.Node) []option {
	var out []option
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Option {
			label := strings.Join(strings.Fields(text(n)), " ")
			value := label
			if hasAttr(n, "value") {
				value = attr(n, "value")
			}
			if !hasAttr(n, "disabled") {
				out = append(out, option{value: value, label: label})
			}
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return out
}

// choose finds the option an answer names, by its label or its value.
func (i *input) choose(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	for _, candidate := range i.options {
		if strings.EqualFold(candidate.label, answer) || strings.EqualFold(candidate.value, answer) {
			return candidate.value, true
		}
	}
	return "", false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}
