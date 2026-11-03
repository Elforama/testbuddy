package js

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/dop251/goja"
)

// A small stand-in for Datafaker: the generators the inputRandom* commands use and the
// faker.* calls flows commonly make. Unknown faker methods return a random word.

var (
	firstNames = strings.Fields("James Mary Robert Patricia John Jennifer Michael Linda David Elizabeth William Barbara Richard Susan Joseph Jessica Thomas Sarah Charles Karen Daniel Lisa Matthew Nancy Anthony Betty Mark Sandra Paul Ashley Steven Emily Andrew Donna Joshua Michelle Kevin Carol Brian Amanda Olivia Liam Emma Noah Ava Lucas Mia Ethan Zoe Leo")
	lastNames  = strings.Fields("Smith Johnson Williams Brown Jones Garcia Miller Davis Rodriguez Martinez Hernandez Lopez Gonzalez Wilson Anderson Thomas Taylor Moore Jackson Martin Lee Perez Thompson White Harris Sanchez Clark Ramirez Lewis Robinson Walker Young Allen King Wright Scott Torres Nguyen Hill Flores")
	cities     = append(strings.Fields("Springfield Riverside Franklin Greenville Bristol Clinton Fairview Salem Madison Georgetown Arlington Ashland Dover Oxford Jackson Burlington Manchester Milton Newport Auburn Dayton Lexington Milford Winchester Hudson Kingston"), "Mount Vernon")
	countries  = []string{"France", "Japan", "Brazil", "Canada", "Kenya", "Norway", "Chile", "India", "Portugal", "Vietnam", "Egypt", "Mexico", "Iceland", "New Zealand", "Ghana", "Peru", "Sweden", "Morocco", "Ireland", "Thailand"}
	colors     = strings.Fields("red green blue yellow purple orange pink brown black white gray teal indigo violet maroon olive navy cyan magenta lime")
	words      = strings.Fields("alpha bravo river stone cloud maple ember quartz harbor willow canyon meadow falcon orbit lantern velvet copper tundra summit prairie")
	domains    = strings.Fields("example.com example.org example.net mail.test inbox.test")
)

func pick(list []string) string { return list[rand.IntN(len(list))] }

// RandomText is `length` random lowercase letters.
func RandomText(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = byte('a' + rand.IntN(26))
	}
	return string(b)
}

// RandomNumber is a number with `length` digits (no leading zero).
func RandomNumber(length int) string {
	b := make([]byte, length)
	for i := range b {
		d := rand.IntN(10)
		if i == 0 && length > 1 {
			d = 1 + rand.IntN(9)
		}
		b[i] = byte('0' + d)
	}
	return string(b)
}

func RandomEmail() string {
	return strings.ToLower(pick(firstNames)) + "." + strings.ToLower(pick(lastNames)) + fmt.Sprint(rand.IntN(100)) + "@" + pick(domains)
}
func RandomPersonName() string { return pick(firstNames) + " " + pick(lastNames) }
func RandomCity() string       { return pick(cities) }
func RandomCountry() string    { return pick(countries) }
func RandomColor() string      { return pick(colors) }

// newFaker builds a `faker` object: faker.name().firstName(), faker.internet().emailAddress(), ...
func newFaker(rt *goja.Runtime) *goja.Object {
	group := func(methods map[string]func() any) func() *goja.Object {
		return func() *goja.Object {
			o := rt.NewObject()
			for k, f := range methods {
				f := f
				o.Set(k, func(call goja.FunctionCall) goja.Value { return rt.ToValue(f()) })
			}
			return o
		}
	}
	f := rt.NewObject()
	f.Set("name", group(map[string]func() any{
		"firstName": func() any { return pick(firstNames) },
		"lastName":  func() any { return pick(lastNames) },
		"fullName":  func() any { return RandomPersonName() },
		"name":      func() any { return RandomPersonName() },
		"username":  func() any { return strings.ToLower(pick(firstNames)) + fmt.Sprint(rand.IntN(1000)) },
	}))
	f.Set("internet", group(map[string]func() any{
		"emailAddress":     func() any { return RandomEmail() },
		"safeEmailAddress": func() any { return RandomEmail() },
		"password":         func() any { return RandomText(6) + RandomNumber(3) + "A!" },
		"url":              func() any { return "https://" + pick(domains) + "/" + pick(words) },
		"domainName":       func() any { return pick(domains) },
	}))
	f.Set("address", group(map[string]func() any{
		"city":          func() any { return RandomCity() },
		"cityName":      func() any { return RandomCity() },
		"country":       func() any { return RandomCountry() },
		"streetAddress": func() any { return fmt.Sprintf("%d %s Street", 1+rand.IntN(9999), pick(lastNames)) },
		"zipCode":       func() any { return RandomNumber(5) },
	}))
	f.Set("color", group(map[string]func() any{"name": func() any { return RandomColor() }}))
	f.Set("lorem", group(map[string]func() any{
		"word": func() any { return pick(words) },
		"sentence": func() any {
			first := pick(words)
			return strings.ToUpper(first[:1]) + first[1:] + " " + pick(words) + " " + pick(words) + "."
		},
	}))
	f.Set("number", func() *goja.Object {
		o := rt.NewObject()
		o.Set("numberBetween", func(a, b int64) int64 {
			if b <= a {
				return a
			}
			return a + rand.Int64N(b-a)
		})
		o.Set("randomNumber", func(call goja.FunctionCall) goja.Value {
			n := 8
			if len(call.Arguments) > 0 {
				n = int(call.Arguments[0].ToInteger())
			}
			return rt.ToValue(RandomNumber(max(1, n)))
		})
		o.Set("randomDigit", func() int { return rand.IntN(10) })
		return o
	})
	f.Set("phoneNumber", group(map[string]func() any{
		"phoneNumber": func() any { return "555-" + RandomNumber(3) + "-" + RandomNumber(4) },
		"cellPhone":   func() any { return "555-" + RandomNumber(3) + "-" + RandomNumber(4) },
	}))
	// Any other faker.<name>() returns a random word instead of throwing.
	p := rt.NewProxy(f, &goja.ProxyTrapConfig{
		Get: func(target *goja.Object, property string, receiver goja.Value) goja.Value {
			if v := target.Get(property); v != nil {
				return v
			}
			return rt.ToValue(func() string { return pick(words) })
		},
	})
	return rt.ToValue(p).ToObject(rt)
}
