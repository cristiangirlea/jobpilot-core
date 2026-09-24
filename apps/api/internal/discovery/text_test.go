package discovery

import "testing"

func TestReadableKeepsTheShapeOfAPosting(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"paragraphs, a list and entities",
			`<p><strong>Who we are:</strong></p><p>We build tools.</p><ul><li>Go</li><li>Kubernetes</li></ul><p>Pay &amp; perks&nbsp;included</p>`,
			"Who we are:\n\nWe build tools.\n\n• Go\n• Kubernetes\n\nPay & perks included"},
		{"html that arrives escaped, as Greenhouse sends it",
			`&lt;p&gt;Hello &amp;amp; welcome&lt;/p&gt;&lt;ul&gt;&lt;li&gt;First&lt;/li&gt;&lt;/ul&gt;`,
			"Hello & welcome\n\n• First"},
		{"line breaks, headings and nested blocks",
			"<h2>Benefits</h2><div><div><p>Line one<br>Line two<br/></p></div></div>",
			"Benefits\n\nLine one\nLine two"},
		{"scripts and styles are not text",
			"<style>p{color:red}</style><p>Visible</p><script>alert(1)</script>",
			"Visible"},
		{"table cells stay apart",
			"<table><tr><td>Salary</td><td>60k</td></tr></table>",
			"Salary 60k"},
		{"plain text keeps its line breaks",
			"First line\r\nSecond   line\n\n\n\nLast",
			"First line\nSecond line\n\nLast"},
		{"empty list items and stray spaces vanish",
			"<ul><li> </li><li>  Real item </li></ul>",
			"• Real item"},
		{"a comparison is not a tag",
			"<p>5 &lt; 10 and 10 &gt; 5</p>",
			"5 < 10 and 10 > 5"},
	}
	for _, c := range cases {
		if got := Readable(c.in); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}
