package js

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const draws = 200 // random generators are checked over many draws

func inList(list []string, s string) bool { return slices.Contains(list, s) }

// RandomText is exactly `length` lowercase ASCII letters.
func TestRandomText(t *testing.T) {
	for _, n := range []int{0, 1, 5, 64} {
		for i := 0; i < draws; i++ {
			s := RandomText(n)
			if len(s) != n || !regexp.MustCompile(`^[a-z]*$`).MatchString(s) {
				t.Fatalf("RandomText(%d) = %q", n, s)
			}
		}
	}
}

// RandomNumber has `length` digits and no leading zero unless it is a single digit.
func TestRandomNumber(t *testing.T) {
	if s := RandomNumber(0); s != "" {
		t.Errorf("RandomNumber(0) = %q", s)
	}
	sawZero := false
	for i := 0; i < 2000; i++ {
		s := RandomNumber(1)
		if len(s) != 1 || s[0] < '0' || s[0] > '9' {
			t.Fatalf("RandomNumber(1) = %q", s)
		}
		sawZero = sawZero || s == "0"
	}
	if !sawZero {
		t.Error("RandomNumber(1) never produced 0 in 2000 draws")
	}
	for _, n := range []int{2, 5, 18} {
		for i := 0; i < draws; i++ {
			s := RandomNumber(n)
			if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(s) || len(s) != n {
				t.Fatalf("RandomNumber(%d) = %q", n, s)
			}
		}
	}
}

var emailRE = regexp.MustCompile(`^[a-z]+\.[a-z]+[0-9]{1,2}@(example\.(com|org|net)|mail\.test|inbox\.test)$`)

// RandomEmail is first.last<0-99>@<a test domain>, all lowercase.
func TestRandomEmail(t *testing.T) {
	for i := 0; i < draws; i++ {
		if s := RandomEmail(); !emailRE.MatchString(s) {
			t.Fatalf("RandomEmail() = %q", s)
		}
	}
}

// RandomPersonName is "First Last" from the name lists.
func TestRandomPersonName(t *testing.T) {
	for i := 0; i < draws; i++ {
		first, last, ok := strings.Cut(RandomPersonName(), " ")
		if !ok || !inList(firstNames, first) || !inList(lastNames, last) {
			t.Fatalf("RandomPersonName() = %q %q", first, last)
		}
	}
}

// RandomCity, RandomCountry and RandomColor draw from their lists.
func TestRandomPicks(t *testing.T) {
	for i := 0; i < draws; i++ {
		if c := RandomCity(); !inList(cities, c) {
			t.Fatalf("RandomCity() = %q", c)
		}
		if c := RandomCountry(); !inList(countries, c) {
			t.Fatalf("RandomCountry() = %q", c)
		}
		if c := RandomColor(); !inList(colors, c) {
			t.Fatalf("RandomColor() = %q", c)
		}
	}
	// Generators are actually random: many draws give more than one value.
	seen := map[string]bool{}
	for i := 0; i < draws; i++ {
		seen[RandomColor()] = true
	}
	if len(seen) < 2 {
		t.Errorf("RandomColor gave only %v", seen)
	}
}

// Multi-word place names stay whole: "Mount Vernon" is one city, not two.
func TestCityListKeepsMultiWordNames(t *testing.T) {
	if inList(cities, "Mount") || inList(cities, "Vernon") || !inList(cities, "Mount Vernon") {
		t.Error("cities splits or lacks Mount Vernon")
	}
}

// pick returns an element of the list.
func TestPick(t *testing.T) {
	if got := pick([]string{"only"}); got != "only" {
		t.Errorf("pick = %q", got)
	}
	l := []string{"a", "b", "c"}
	for i := 0; i < draws; i++ {
		if !inList(l, pick(l)) {
			t.Fatal("pick returned a value outside the list")
		}
	}
}

// evalString runs src and returns its display string.
func evalString(t *testing.T, e *Engine, src string) string {
	t.Helper()
	v, err := e.Eval(src, nil)
	if err != nil {
		t.Fatalf("Eval(%q): %v", src, err)
	}
	return Display(v)
}

// The faker object's generators produce values of the right shape.
func TestFakerObject(t *testing.T) {
	e := New("android")
	digits := regexp.MustCompile(`^[0-9]+$`)
	checks := []struct {
		src string
		ok  func(string) bool
	}{
		{"faker.name().firstName()", func(s string) bool { return inList(firstNames, s) }},
		{"faker.name().lastName()", func(s string) bool { return inList(lastNames, s) }},
		{"faker.name().fullName()", func(s string) bool {
			f, l, ok := strings.Cut(s, " ")
			return ok && inList(firstNames, f) && inList(lastNames, l)
		}},
		{"faker.name().name()", func(s string) bool { return strings.Count(s, " ") == 1 }},
		{"faker.name().username()", regexp.MustCompile(`^[a-z]+[0-9]{1,3}$`).MatchString},
		{"faker.internet().emailAddress()", emailRE.MatchString},
		{"faker.internet().safeEmailAddress()", emailRE.MatchString},
		{"faker.internet().password()", regexp.MustCompile(`^[a-z]{6}[1-9][0-9]{2}A!$`).MatchString},
		{"faker.internet().url()", func(s string) bool {
			rest, ok := strings.CutPrefix(s, "https://")
			dom, word, ok2 := strings.Cut(rest, "/")
			return ok && ok2 && inList(domains, dom) && inList(words, word)
		}},
		{"faker.internet().domainName()", func(s string) bool { return inList(domains, s) }},
		{"faker.address().city()", func(s string) bool { return inList(cities, s) }},
		{"faker.address().cityName()", func(s string) bool { return inList(cities, s) }},
		{"faker.address().country()", func(s string) bool { return inList(countries, s) }},
		{"faker.address().streetAddress()", func(s string) bool {
			m := regexp.MustCompile(`^([0-9]{1,4}) ([A-Za-z]+) Street$`).FindStringSubmatch(s)
			if m == nil {
				return false
			}
			n, _ := strconv.Atoi(m[1])
			return n >= 1 && n <= 9999 && inList(lastNames, m[2])
		}},
		{"faker.address().zipCode()", regexp.MustCompile(`^[1-9][0-9]{4}$`).MatchString},
		{"faker.color().name()", func(s string) bool { return inList(colors, s) }},
		{"faker.lorem().word()", func(s string) bool { return inList(words, s) }},
		{"faker.number().randomDigit()", regexp.MustCompile(`^[0-9]$`).MatchString},
		{"faker.number().randomNumber()", func(s string) bool { return len(s) == 8 && digits.MatchString(s) }},
		{"faker.number().randomNumber(3)", func(s string) bool { return len(s) == 3 && digits.MatchString(s) }},
		{"faker.number().randomNumber(0)", func(s string) bool { return len(s) == 1 && digits.MatchString(s) }},
		{"faker.number().randomNumber(-4)", func(s string) bool { return len(s) == 1 && digits.MatchString(s) }},
		{"typeof faker.number().randomNumber(4)", func(s string) bool { return s == "string" }},
		{"faker.phoneNumber().phoneNumber()", regexp.MustCompile(`^555-[1-9][0-9]{2}-[1-9][0-9]{3}$`).MatchString},
		{"faker.phoneNumber().cellPhone()", regexp.MustCompile(`^555-[1-9][0-9]{2}-[1-9][0-9]{3}$`).MatchString},
	}
	for _, c := range checks {
		t.Run(c.src, func(t *testing.T) {
			for i := 0; i < 50; i++ {
				if got := evalString(t, e, c.src); !c.ok(got) {
					t.Fatalf("%s = %q", c.src, got)
				}
			}
		})
	}
}

// numberBetween returns a in [a, b), or a when the range is empty or reversed.
func TestFakerNumberBetween(t *testing.T) {
	e := New("android")
	for i := 0; i < draws; i++ {
		v, err := e.Eval("faker.number().numberBetween(5, 8)", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := v.ToInteger(); n < 5 || n >= 8 {
			t.Fatalf("numberBetween(5, 8) = %d", n)
		}
	}
	for _, src := range []string{"faker.number().numberBetween(9, 9)", "faker.number().numberBetween(9, 3)"} {
		if got := evalString(t, e, src); got != "9" {
			t.Errorf("%s = %q, want 9", src, got)
		}
	}
	if got := evalString(t, e, "faker.number().numberBetween(-3, -2)"); got != "-3" {
		t.Errorf("numberBetween(-3, -2) = %q", got)
	}
	if got := evalString(t, e, "typeof faker.number().numberBetween(1, 100)"); got != "number" {
		t.Errorf("numberBetween type = %q", got)
	}
}

// lorem().sentence() is three or more listed words, capitalized, ending in a period.
func TestFakerSentence(t *testing.T) {
	e := New("android")
	bad := ""
	for i := 0; i < 50; i++ {
		s := evalString(t, e, "faker.lorem().sentence()")
		if !strings.HasSuffix(s, ".") {
			t.Fatalf("sentence %q has no final period", s)
		}
		ws := strings.Fields(strings.TrimSuffix(s, "."))
		if len(ws) != 3 {
			t.Fatalf("sentence %q: %d words", s, len(ws))
		}
		if ws[0][:1] != strings.ToUpper(ws[0][:1]) {
			t.Fatalf("sentence %q is not capitalized", s)
		}
		for _, w := range ws[1:] {
			if !inList(words, w) {
				t.Fatalf("sentence %q: %q is not a lorem word", s, w)
			}
		}
		if !inList(words, strings.ToLower(ws[0])) {
			bad = s
		}
	}
	if bad != "" {
		t.Errorf("sentence %q starts with a word that is not a lorem word", bad)
	}
}

// Each group call returns a fresh object with the expected method set.
func TestFakerGroups(t *testing.T) {
	e := New("android")
	cases := map[string]string{
		"Object.keys(faker.name()).sort().join(',')":        "firstName,fullName,lastName,name,username",
		"Object.keys(faker.internet()).sort().join(',')":    "domainName,emailAddress,password,safeEmailAddress,url",
		"Object.keys(faker.address()).sort().join(',')":     "city,cityName,country,streetAddress,zipCode",
		"Object.keys(faker.color()).join(',')":              "name",
		"Object.keys(faker.lorem()).sort().join(',')":       "sentence,word",
		"Object.keys(faker.number()).sort().join(',')":      "numberBetween,randomDigit,randomNumber",
		"Object.keys(faker.phoneNumber()).sort().join(',')": "cellPhone,phoneNumber",
		"faker.name() === faker.name()":                     "false",
	}
	for src, want := range cases {
		if got := evalString(t, e, src); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
}

// faker output interpolates into flow values like any other expression.
func TestFakerInInterpolate(t *testing.T) {
	e := New("android")
	got, err := e.Interpolate("user-${faker.number().randomNumber(4)}@${faker.internet().domainName()}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^user-[1-9][0-9]{3}@(example\.(com|org|net)|mail\.test|inbox\.test)$`).MatchString(got) {
		t.Errorf("got %q", got)
	}
}

// Unknown faker methods return a random word (as the package documents) instead of throwing.
func TestFakerUnknownMethod(t *testing.T) {
	e := New("android")
	v, err := e.Eval("faker.unknownGenerator()", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !inList(words, Display(v)) {
		t.Errorf("faker.unknownGenerator() = %q", Display(v))
	}
}
