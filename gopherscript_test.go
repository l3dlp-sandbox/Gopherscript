package gopherscript

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMustParseModule(t *testing.T) {

	t.Run("empty module", func(t *testing.T) {
		n := MustParseModule("")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 0}},
		}, n)
	})

	t.Run("empty module with empty requirements", func(t *testing.T) {
		n := MustParseModule("require {}")
		assert.EqualValues(t, &Module{
			NodeBase:   NodeBase{NodeSpan{0, 10}},
			Statements: nil,
			Requirements: &Requirements{
				Object: &ObjectLiteral{
					NodeBase:   NodeBase{NodeSpan{8, 10}},
					Properties: nil,
				},
			},
		}, n)
	})

	t.Run("empty const declarations", func(t *testing.T) {
		n := MustParseModule("const ()")
		assert.EqualValues(t, &Module{
			NodeBase:     NodeBase{NodeSpan{0, 8}},
			Statements:   nil,
			Requirements: nil,
			GlobalConstantDeclarations: &GlobalConstantDeclarations{
				NodeBase:    NodeBase{NodeSpan{0, 8}},
				NamesValues: nil,
			},
		}, n)
	})

	t.Run("const declarations : (single) valid lhs & rhs", func(t *testing.T) {
		n := MustParseModule("const ( $$a = 1 )")
		assert.EqualValues(t, &Module{
			NodeBase:     NodeBase{NodeSpan{0, 17}},
			Statements:   nil,
			Requirements: nil,
			GlobalConstantDeclarations: &GlobalConstantDeclarations{
				NodeBase: NodeBase{NodeSpan{0, 17}},
				NamesValues: [][2]Node{
					{
						&GlobalVariable{
							NodeBase: NodeBase{
								NodeSpan{8, 11},
							},
							Name: "a",
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{14, 15},
							},
							Raw:   "1",
							Value: 1,
						},
					},
				},
			},
		}, n)
	})

	t.Run("variable", func(t *testing.T) {
		n := MustParseModule("$a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&Variable{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Name:     "a",
				},
			},
		}, n)
	})

	t.Run("module with two variables on the same line", func(t *testing.T) {
		n := MustParseModule("$a;$b")
		assert.EqualValues(t, &Module{

			NodeBase: NodeBase{NodeSpan{0, 5}},
			Statements: []Node{
				&Variable{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Name:     "a",
				},
				&Variable{
					NodeBase: NodeBase{NodeSpan{3, 5}},
					Name:     "b",
				},
			}}, n)
	})

	t.Run("identifier", func(t *testing.T) {
		n := MustParseModule("a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 1}},
			Statements: []Node{
				&IdentifierLiteral{
					NodeBase: NodeBase{NodeSpan{0, 1}},
					Name:     "a",
				},
			},
		}, n)
	})

	t.Run("boolean literal : true", func(t *testing.T) {
		n := MustParseModule("true")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&BooleanLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}},
					Value:    true,
				},
			},
		}, n)
	})

	t.Run("boolean literal : false", func(t *testing.T) {
		n := MustParseModule("false")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}},
			Statements: []Node{
				&BooleanLiteral{
					NodeBase: NodeBase{NodeSpan{0, 5}},
					Value:    false,
				},
			},
		}, n)
	})

	t.Run("absolute path literal : /", func(t *testing.T) {
		n := MustParseModule("/")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 1}},
			Statements: []Node{
				&AbsolutePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 1}},
					Value:    "/",
				},
			},
		}, n)
	})

	t.Run("absolute path literal : /a", func(t *testing.T) {
		n := MustParseModule("/a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&AbsolutePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Value:    "/a",
				},
			},
		}, n)
	})

	t.Run("relative path literal : ./", func(t *testing.T) {
		n := MustParseModule("./")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&RelativePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Value:    "./",
				},
			},
		}, n)
	})

	t.Run("relative path literal : ./a", func(t *testing.T) {
		n := MustParseModule("./a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}},
			Statements: []Node{
				&RelativePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}},
					Value:    "./a",
				},
			},
		}, n)
	})

	t.Run("relative path literal in list : [./]", func(t *testing.T) {
		n := MustParseModule("[./]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}},
					Elements: []Node{
						&RelativePathLiteral{
							NodeBase: NodeBase{NodeSpan{1, 3}},
							Value:    "./",
						},
					},
				},
			},
		}, n)
	})

	t.Run("absolute path pattern literal : /a*", func(t *testing.T) {
		n := MustParseModule("/a*")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}},
			Statements: []Node{
				&AbsolutePathPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}},
					Value:    "/a*",
				},
			},
		}, n)
	})

	t.Run("absolute path pattern literal ending with /... ", func(t *testing.T) {
		n := MustParseModule("/a/...")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}},
			Statements: []Node{
				&AbsolutePathPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 6}},
					Value:    "/a/...",
				},
			},
		}, n)
	})

	t.Run("absolute path pattern literal : /... ", func(t *testing.T) {
		n := MustParseModule("/...")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&AbsolutePathPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}},
					Value:    "/...",
				},
			},
		}, n)
	})

	t.Run("absolute path pattern literal containg /... in the middle", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("/a/.../ee")
		})
	})

	t.Run("absolute path expression : single trailing interpolation", func(t *testing.T) {
		n := MustParseModule("/home/$username$")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 16}},
			Statements: []Node{
				&AbsolutePathExpression{
					NodeBase: NodeBase{NodeSpan{0, 16}},
					Slices: []Node{
						&PathSlice{
							NodeBase: NodeBase{
								NodeSpan{0, 6},
							},
							Value: "/home/",
						},
						&Variable{
							NodeBase: NodeBase{
								NodeSpan{6, 16},
							},
							Name: "username",
						},
					},
				},
			},
		}, n)
	})

	t.Run("absolute path expression : single embedded interpolation", func(t *testing.T) {
		n := MustParseModule("/home/$username$/projects")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 25}},
			Statements: []Node{
				&AbsolutePathExpression{
					NodeBase: NodeBase{NodeSpan{0, 25}},
					Slices: []Node{
						&PathSlice{
							NodeBase: NodeBase{
								NodeSpan{0, 6},
							},
							Value: "/home/",
						},
						&Variable{
							NodeBase: NodeBase{
								NodeSpan{6, 16},
							},
							Name: "username",
						},
						&PathSlice{
							NodeBase: NodeBase{
								NodeSpan{16, 25},
							},
							Value: "/projects",
						},
					},
				},
			},
		}, n)
	})

	t.Run("nil literal", func(t *testing.T) {
		n := MustParseModule("nil")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}},
			Statements: []Node{
				&NilLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <single letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 4}},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}},
						Name:     "a",
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{3, 4}},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <two-letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.bc")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 5}},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}},
						Name:     "a",
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{3, 5}},
						Name:     "bc",
					},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <propname> '.' <single-letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.b.c")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 6}},
					Left: &MemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 4}},
						Left: &Variable{
							NodeBase: NodeBase{NodeSpan{0, 2}},
							Name:     "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{3, 4}},
							Name:     "b",
						},
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{5, 6}},
						Name:     "c",
					},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <propname> '.' <singltwtwo-letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.b.cd")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 7}},
					Left: &MemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 4}},
						Left: &Variable{
							NodeBase: NodeBase{NodeSpan{0, 2}},
							Name:     "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{3, 4}},
							Name:     "b",
						},
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{5, 7}},
						Name:     "cd",
					},
				},
			},
		}, n)
	})

	t.Run("parenthesized expression", func(t *testing.T) {
		n := MustParseModule("($a)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&Variable{
					NodeBase: NodeBase{NodeSpan{1, 3}},
					Name:     "a",
				},
			},
		}, n)
	})

	t.Run("member of a parenthesized expression", func(t *testing.T) {
		n := MustParseModule("($a).name")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 9}},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{1, 3}},
						Name:     "a",
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{5, 9}},
						Name:     "name",
					},
				},
			},
		}, n)
	})

	t.Run("index expression : variable '[' <integer literal> '] ", func(t *testing.T) {
		n := MustParseModule("$a[0]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}},
			Statements: []Node{
				&IndexExpression{
					NodeBase: NodeBase{NodeSpan{0, 5}},
					Indexed: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}},
						Name:     "a",
					},
					Index: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{3, 4}},
						Raw:      "0",
						Value:    0,
					},
				},
			},
		}, n)
	})

	t.Run("index expression : <member expression> '[' <integer literal> '] ", func(t *testing.T) {
		n := MustParseModule("$a.b[0]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}},
			Statements: []Node{
				&IndexExpression{
					NodeBase: NodeBase{NodeSpan{0, 7}},
					Indexed: &MemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 4}},
						Left: &Variable{
							NodeBase: NodeBase{NodeSpan{0, 2}},
							Name:     "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{3, 4}},
							Name:     "b",
						},
					},
					Index: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{5, 6}},
						Raw:      "0",
						Value:    0,
					},
				},
			},
		}, n)
	})

	t.Run("slice expression : variable '[' <integer literal> ':' ] ", func(t *testing.T) {
		n := MustParseModule("$a[0:]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}},
			Statements: []Node{
				&SliceExpression{
					NodeBase: NodeBase{NodeSpan{0, 6}},
					Indexed: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}},
						Name:     "a",
					},
					StartIndex: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{3, 4}},
						Raw:      "0",
						Value:    0,
					},
				},
			},
		}, n)
	})

	t.Run("slice expression : variable '['  ':' <integer literal> ] ", func(t *testing.T) {
		n := MustParseModule("$a[:1]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}},
			Statements: []Node{
				&SliceExpression{
					NodeBase: NodeBase{NodeSpan{0, 6}},
					Indexed: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}},
						Name:     "a",
					},
					EndIndex: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{4, 5}},
						Raw:      "1",
						Value:    1,
					},
				},
			},
		}, n)
	})

	t.Run("slice expression : variable '[' ':' ']' : invalid ", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("$a[:]")
		})
	})

	t.Run("slice expression : variable '[' ':' <integer literal> ':' ']' : invalid ", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("$a[:1:]")
		})
	})

	t.Run("key list expression : empty", func(t *testing.T) {
		n := MustParseModule(".{}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}},
			Statements: []Node{
				&KeyListExpression{
					NodeBase: NodeBase{NodeSpan{0, 3}},
					Keys:     nil,
				},
			},
		}, n)
	})

	t.Run("key list expression : one key", func(t *testing.T) {
		n := MustParseModule(".{name}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}},
			Statements: []Node{
				&KeyListExpression{
					NodeBase: NodeBase{NodeSpan{0, 7}},
					Keys: []*IdentifierLiteral{
						{
							NodeBase: NodeBase{
								NodeSpan{2, 6},
							},
							Name: "name",
						},
					},
				},
			},
		}, n)
	})

	t.Run("key list expression : two keys separated by space", func(t *testing.T) {
		n := MustParseModule(".{name age}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 11}},
			Statements: []Node{
				&KeyListExpression{
					NodeBase: NodeBase{NodeSpan{0, 11}},
					Keys: []*IdentifierLiteral{
						{
							NodeBase: NodeBase{
								NodeSpan{2, 6},
							},
							Name: "name",
						},
						{
							NodeBase: NodeBase{
								NodeSpan{7, 10},
							},
							Name: "age",
						},
					},
				},
			},
		}, n)
	})

	t.Run("URL literal : root", func(t *testing.T) {
		n := MustParseModule(`https://example.com/`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 20}},
			Statements: []Node{
				&URLLiteral{
					NodeBase: NodeBase{NodeSpan{0, 20}},
					Value:    "https://example.com/",
				},
			},
		}, n)
	})

	t.Run("URL literal : ends with ..", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule(`https://example.com/..`)
		})
	})

	t.Run("URL literal : two dots in the middle of the path", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule(`https://example.com/../users`)
		})
	})

	t.Run("URL pattern literal : prefix pattern, root", func(t *testing.T) {
		n := MustParseModule(`https://example.com/...`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 23}},
			Statements: []Node{
				&URLPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 23}},
					Value:    "https://example.com/...",
				},
			},
		}, n)
	})

	t.Run("URL pattern literal : prefix pattern containing two dots (invalid)", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule(`https://example.com/../...`)
		})
	})

	t.Run("HTTP host", func(t *testing.T) {
		n := MustParseModule(`https://example.com`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 19}},
			Statements: []Node{
				&HTTPHostLiteral{
					NodeBase: NodeBase{NodeSpan{0, 19}},
					Value:    "https://example.com",
				},
			},
		}, n)
	})

	t.Run("URL literal ending containing a ')'", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule(`https://example.com)`)
		})
	})

	t.Run("HTTP host pattern : https://*", func(t *testing.T) {
		n := MustParseModule(`https://*`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}},
			Statements: []Node{
				&HTTPHostPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 9}},
					Value:    "https://*",
				},
			},
		}, n)
	})

	t.Run("HTTP host pattern : https://*:443", func(t *testing.T) {
		n := MustParseModule(`https://*:443`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 13}},
			Statements: []Node{
				&HTTPHostPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 13}},
					Value:    "https://*:443",
				},
			},
		}, n)
	})

	t.Run("HTTP host pattern : https://*.<tld>", func(t *testing.T) {
		n := MustParseModule(`https://*.com`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 13}},
			Statements: []Node{
				&HTTPHostPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 13}},
					Value:    "https://*.com",
				},
			},
		}, n)
	})

	t.Run("invalid HTTP host pattern : https://*.128", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule(`https://*128`)
		})
	})

	t.Run("URL expression : no query, single trailing interpolation", func(t *testing.T) {
		n := MustParseModule(`https://example.com/$path$`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 26}},
			Statements: []Node{
				&URLExpression{
					NodeBase: NodeBase{NodeSpan{0, 26}},
					Raw:      "https://example.com/$path$",
					HostPart: "https://example.com",
					Path: &AbsolutePathExpression{
						NodeBase: NodeBase{NodeSpan{19, 26}},
						Slices: []Node{
							&PathSlice{
								NodeBase: NodeBase{
									NodeSpan{19, 20},
								},
								Value: "/",
							},
							&Variable{
								NodeBase: NodeBase{
									NodeSpan{20, 26},
								},
								Name: "path",
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("integer literal", func(t *testing.T) {
		n := MustParseModule("12")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&IntLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Raw:      "12",
					Value:    12,
				},
			},
		}, n)
	})

	t.Run("float literal", func(t *testing.T) {
		n := MustParseModule("12.0")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&FloatLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}},
					Raw:      "12.0",
					Value:    12.0,
				},
			},
		}, n)
	})

	t.Run("quantity literal : integer", func(t *testing.T) {
		n := MustParseModule("1s")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&QuantityLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Raw:      "1s",
					Unit:     "s",
					Value:    1.0,
				},
			},
		}, n)
	})

	t.Run("quantity literal : float", func(t *testing.T) {
		n := MustParseModule("1.5s")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&QuantityLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}},
					Raw:      "1.5s",
					Unit:     "s",
					Value:    1.5,
				},
			},
		}, n)
	})

	t.Run("rate literal", func(t *testing.T) {
		n := MustParseModule("1kB/s")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}},
			Statements: []Node{
				&RateLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
					},
					Quantity: &QuantityLiteral{
						NodeBase: NodeBase{NodeSpan{0, 3}},
						Raw:      "1kB",
						Unit:     "kB",
						Value:    1.0,
					},
					Unit: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{4, 5},
						},
						Name: "s",
					},
				},
			},
		}, n)
	})

	t.Run("unterminated rate literal", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("1kB/")
		})
	})

	t.Run("empty string literal", func(t *testing.T) {
		n := MustParseModule(`""`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Raw:      `""`,
					Value:    ``,
				},
			},
		}, n)
	})

	t.Run("string literal : single space", func(t *testing.T) {
		n := MustParseModule(`" "`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}},
					Raw:      `" "`,
					Value:    ` `,
				},
			},
		}, n)
	})

	t.Run("string literal : single, non ASCII character", func(t *testing.T) {
		n := MustParseModule(`"é"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}},
					Raw:      `"é"`,
					Value:    `é`,
				},
			},
		}, n)
	})

	t.Run("string literal : one escaped backslashe", func(t *testing.T) {
		n := MustParseModule(`"\\"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}},
					Raw:      `"\\"`,
					Value:    `\`,
				},
			},
		}, n)
	})

	t.Run("string literal : two escaped backslashes", func(t *testing.T) {
		n := MustParseModule(`"\\\\"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 6}},
					Raw:      `"\\\\"`,
					Value:    `\\`,
				},
			},
		}, n)
	})

	t.Run("identifier literal : single letter", func(t *testing.T) {
		n := MustParseModule(`e`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 1}},
			Statements: []Node{
				&IdentifierLiteral{
					NodeBase: NodeBase{NodeSpan{0, 1}},
					Name:     "e",
				},
			},
		}, n)
	})

	t.Run("identifier literal : letter followed by a digit", func(t *testing.T) {
		n := MustParseModule(`e2`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&IdentifierLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}},
					Name:     "e2",
				},
			},
		}, n)
	})

	t.Run("assignment var = <value>", func(t *testing.T) {
		n := MustParseModule("$a = $b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}},
			Statements: []Node{
				&Assignment{
					NodeBase: NodeBase{NodeSpan{0, 7}},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}},
						Name:     "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{5, 7}},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("assignment <index expr> = <value>", func(t *testing.T) {
		n := MustParseModule("$a[0] = $b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}},
			Statements: []Node{
				&Assignment{
					NodeBase: NodeBase{NodeSpan{0, 10}},
					Left: &IndexExpression{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Indexed: &Variable{
							NodeBase: NodeBase{
								NodeSpan{0, 2},
							},
							Name: "a",
						},
						Index: &IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{3, 4},
							},
							Raw:   "0",
							Value: 0,
						},
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{8, 10}},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("multi assignement statement : assign var = var", func(t *testing.T) {
		n := MustParseModule("assign $a = $b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 14}},
			Statements: []Node{
				&MultiAssignment{
					NodeBase: NodeBase{NodeSpan{0, 14}},
					Variables: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{7, 9}},
							Name:     "a",
						},
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{12, 14}},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("multi assignement statement : assign var var = var", func(t *testing.T) {
		n := MustParseModule("assign $a $b = $c")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 17}},
			Statements: []Node{
				&MultiAssignment{
					NodeBase: NodeBase{NodeSpan{0, 17}},
					Variables: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{7, 9}},
							Name:     "a",
						},
						&Variable{
							NodeBase: NodeBase{NodeSpan{10, 12}},
							Name:     "b",
						},
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{15, 17}},
						Name:     "c",
					},
				},
			},
		}, n)
	})

	t.Run("call with paren : no args", func(t *testing.T) {
		n := MustParseModule("print()")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 7}},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: nil,
				},
			},
		}, n)
	})

	t.Run("call with paren : no args 2", func(t *testing.T) {
		n := MustParseModule("print( )")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 8}},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: nil,
				},
			},
		}, n)
	})

	t.Run("call : single arg", func(t *testing.T) {
		n := MustParseModule("print($a)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 9}},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}},
							Name:     "a",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call with paren: two args", func(t *testing.T) {
		n := MustParseModule("print($a $b)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 12}},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 12}},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}},
							Name:     "a",
						},
						&Variable{
							NodeBase: NodeBase{NodeSpan{9, 11}},
							Name:     "b",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call without paren: one arg", func(t *testing.T) {
		n := MustParseModule("print $a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}},
			Statements: []Node{
				&Call{
					Must:     true,
					NodeBase: NodeBase{NodeSpan{0, 8}},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}},
							Name:     "a",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call without paren: two args", func(t *testing.T) {
		n := MustParseModule("print $a $b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 11}},
			Statements: []Node{
				&Call{
					Must:     true,
					NodeBase: NodeBase{NodeSpan{0, 11}},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}},
							Name:     "a",
						},
						&Variable{
							NodeBase: NodeBase{NodeSpan{9, 11}},
							Name:     "b",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call without paren: followed by a single line comment", func(t *testing.T) {
		n := MustParseModule("print $a $b # comment")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 21}},
			Statements: []Node{
				&Call{
					Must:     true,
					NodeBase: NodeBase{NodeSpan{0, 11}},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}},
							Name:     "a",
						},
						&Variable{
							NodeBase: NodeBase{NodeSpan{9, 11}},
							Name:     "b",
						},
					},
				},
			},
		}, n)
	})

	t.Run("identifier member expression", func(t *testing.T) {
		n := MustParseModule("http.get")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}},
			Statements: []Node{
				&IdentifierMemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 8}},
					Left: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 4}},
						Name:     "http",
					},
					PropertyNames: []*IdentifierLiteral{
						{
							NodeBase: NodeBase{NodeSpan{5, 8}},
							Name:     "get",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call <string> shorthand", func(t *testing.T) {
		n := MustParseModule(`mime"json"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 10}},
					Must:     true,
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 4}},
						Name:     "mime",
					},
					Arguments: []Node{
						&StringLiteral{
							NodeBase: NodeBase{NodeSpan{4, 10}},
							Raw:      `"json"`,
							Value:    "json",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call with paren : identifier member expression", func(t *testing.T) {
		n := MustParseModule("http.get()")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 10}},
					Callee: &IdentifierMemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 8}},
						Left: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{0, 4}},
							Name:     "http",
						},
						PropertyNames: []*IdentifierLiteral{
							{
								NodeBase: NodeBase{NodeSpan{5, 8}},
								Name:     "get",
							},
						},
					},
					Arguments: nil,
				},
			},
		}, n)
	})

	t.Run("call without paren : callee is an identifier member expression", func(t *testing.T) {
		n := MustParseModule(`a.b "a"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 7},
			},
			Statements: []Node{
				&Call{
					Must: true,
					NodeBase: NodeBase{
						NodeSpan{0, 7},
					},
					Callee: &IdentifierMemberExpression{
						NodeBase: NodeBase{
							NodeSpan{0, 3},
						},
						Left: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{0, 1},
							},
							Name: "a",
						},
						PropertyNames: []*IdentifierLiteral{
							{
								NodeBase: NodeBase{
									NodeSpan{2, 3},
								},
								Name: "b",
							},
						},
					},
					Arguments: []Node{
						&StringLiteral{
							NodeBase: NodeBase{
								NodeSpan{4, 7},
							},
							Raw:   `"a"`,
							Value: "a",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call with paren : callee is a member expression", func(t *testing.T) {
		n := MustParseModule(`$a.b("a")`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 9},
			},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
					},
					Callee: &MemberExpression{
						NodeBase: NodeBase{
							NodeSpan{0, 4},
						},
						Left: &Variable{
							NodeBase: NodeBase{
								NodeSpan{0, 2},
							},
							Name: "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{3, 4},
							},
							Name: "b",
						},
					},
					Arguments: []Node{
						&StringLiteral{
							NodeBase: NodeBase{
								NodeSpan{5, 8},
							},
							Raw:   `"a"`,
							Value: "a",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call expression with no paren : single argument", func(t *testing.T) {
		n := MustParseModule("print$ 1")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 8}},
					Must:     true,
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}},
						Name:     "print",
					},
					Arguments: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{7, 8},
							},
							Raw:   "1",
							Value: 1,
						},
					},
				},
			},
		}, n)
	})

	t.Run("call expression with no paren : no argument", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("print$ ")
		})
	})

	t.Run("empty single linge object literal 1", func(t *testing.T) {
		n := MustParseModule("{}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase:   NodeBase{NodeSpan{0, 2}},
					Properties: nil,
				},
			},
		}, n)
	})

	t.Run("empty single linge object literal 2", func(t *testing.T) {
		n := MustParseModule("{ }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase:   NodeBase{NodeSpan{0, 3}},
					Properties: nil,
				},
			},
		}, n)
	})

	t.Run("single line object literal { ident: integer} ", func(t *testing.T) {
		n := MustParseModule("{ a : 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 9}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}},
								Raw:      "1",
								Value:    1,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("object literal with a too long key ", func(t *testing.T) {
		s := strings.ReplaceAll("{ a : 1 }", "a", strings.Repeat("a", MAX_OBJECT_KEY_BYTE_LEN+1))

		assert.Panics(t, func() {
			MustParseModule(s)
		})
	})

	t.Run("single line object literal { : integer} ", func(t *testing.T) {
		n := MustParseModule("{ : 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 7}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 5}},
							Key:      nil,
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{4, 5}},
								Raw:      "1",
								Value:    1,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line object literal { ident : integer ident : integer } ", func(t *testing.T) {
		n := MustParseModule("{ a : 1  b : 2 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 16}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 16}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{9, 14}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{9, 10}},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{13, 14}},
								Raw:      "2",
								Value:    2,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line object literal { ident : integer , ident : integer } ", func(t *testing.T) {
		n := MustParseModule("{ a : 1 , b : 2 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 17}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 17}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{10, 15}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{10, 11}},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{14, 15}},
								Raw:      "2",
								Value:    2,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line object literal { ident, ident: integer } ", func(t *testing.T) {
		n := MustParseModule("{ a, b: 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 11}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 11}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 9}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{8, 9}},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{2, 9}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{5, 6}},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{8, 9}},
								Raw:      "1",
								Value:    1,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("multiline object literal { ident : integer <newline> } ", func(t *testing.T) {
		n := MustParseModule("{ a : 1 \n }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 11}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 11}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}},
								Raw:      "1",
								Value:    1,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("multiline object literal { <newline> ident : integer } ", func(t *testing.T) {
		n := MustParseModule("{ \n a : 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 11}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 11}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{4, 9}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{4, 5}},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{8, 9}},
								Raw:      "1",
								Value:    1,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("multiline object literal { ident : integer <newline> ident : integer } ", func(t *testing.T) {
		n := MustParseModule("{ a : 1 \n b : 2 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 17}},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 17}},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{10, 15}},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{10, 11}},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{14, 15}},
								Raw:      "2",
								Value:    2,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("empty single linge list literal 1", func(t *testing.T) {
		n := MustParseModule("[]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 2},
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 2},
					},
					Elements: nil,
				},
			},
		}, n)
	})

	t.Run("empty single linge list literal 2", func(t *testing.T) {
		n := MustParseModule("[ ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 3},
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 3},
					},
					Elements: nil,
				},
			},
		}, n)
	})

	t.Run("single line list literal [ integer ] ", func(t *testing.T) {
		n := MustParseModule("[ 1 ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 5},
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
					},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
							},
							Raw:   "1",
							Value: 1,
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line list literal [ integer integer ] ", func(t *testing.T) {
		n := MustParseModule("[ 1 2 ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 7},
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 7},
					}, Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
							},
							Raw:   "1",
							Value: 1,
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{4, 5},
							},
							Raw:   "2",
							Value: 2,
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line list literal [ integer , integer ] ", func(t *testing.T) {
		n := MustParseModule("[ 1 , 2 ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 9},
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
					},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
							},
							Raw:   "1",
							Value: 1,
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{6, 7},
							},
							Raw:   "2",
							Value: 2,
						},
					},
				},
			},
		}, n)
	})

	t.Run("multiline list literal [ integer <newline> integer ] ", func(t *testing.T) {
		n := MustParseModule("[ 1 \n 2 ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 9},
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
					},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
							},
							Raw:   "1",
							Value: 1,
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{6, 7},
							},
							Raw:   "2",
							Value: 2,
						},
					},
				},
			},
		}, n)
	})

	//also used for checking block parsing
	t.Run("single line empty if statement", func(t *testing.T) {
		n := MustParseModule("if true { }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 11},
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 11},
					}, Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 11},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	//also used for checking block parsing
	t.Run("single line non empty if statement", func(t *testing.T) {
		n := MustParseModule("if true { 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 13},
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 13},
					}, Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 13},
						},
						Statements: []Node{
							&IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{10, 11},
								},
								Raw:   "1",
								Value: 1,
							},
						},
					},
				},
			},
		}, n)
	})

	//also used for checking call parsing
	t.Run("single line if statement containing a call without parenthesis", func(t *testing.T) {
		n := MustParseModule("if true { a 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 15},
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 15},
					},
					Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 15},
						},
						Statements: []Node{
							&Call{
								Must: true,
								NodeBase: NodeBase{
									NodeSpan{10, 13},
								},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{
										NodeSpan{10, 11},
									},
									Name: "a",
								},
								Arguments: []Node{
									&IntLiteral{
										NodeBase: NodeBase{
											NodeSpan{12, 13},
										},
										Raw:   `1`,
										Value: 1,
									},
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("multiline if statement", func(t *testing.T) {
		n := MustParseModule("if true { \n }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 13},
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 13},
					},
					Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 13},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("single line empty if-else statement", func(t *testing.T) {
		n := MustParseModule("if true { } else {}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 19},
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 19},
					}, Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 11},
						},
						Statements: nil,
					},
					Alternate: &Block{
						NodeBase: NodeBase{
							NodeSpan{17, 19},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("single line empty for .. in statement", func(t *testing.T) {
		n := MustParseModule("for $i, $u in $users { }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 24},
			},
			Statements: []Node{
				&ForStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 24},
					},
					KeyIndexVar: &Variable{
						NodeBase: NodeBase{
							NodeSpan{4, 6},
						},
						Name: "i",
					},
					ValueElemVar: &Variable{
						NodeBase: NodeBase{
							NodeSpan{8, 10},
						},
						Name: "u",
					},
					IteratedValue: &Variable{
						NodeBase: NodeBase{
							NodeSpan{14, 20},
						},
						Name: "users",
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{21, 24},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("single line empty for <expr> statement", func(t *testing.T) {
		n := MustParseModule("for (1 .. 2) { }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 16},
			},
			Statements: []Node{
				&ForStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 16},
					},
					KeyIndexVar:  nil,
					ValueElemVar: nil,
					IteratedValue: &BinaryExpression{
						NodeBase: NodeBase{
							NodeSpan{4, 12},
						},
						Operator: Range,
						Left: &IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{5, 6},
							},
							Raw:   "1",
							Value: 1,
						},
						Right: &IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{10, 11},
							},
							Raw:   "2",
							Value: 2,
						},
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{13, 16},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("binary expression", func(t *testing.T) {
		n := MustParseModule("($a + $b)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 9},
			},
			Statements: []Node{
				&BinaryExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
					},
					Operator: Add,
					Left: &Variable{
						NodeBase: NodeBase{
							NodeSpan{1, 3},
						},
						Name: "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{
							NodeSpan{6, 8},
						},
						Name: "b",
					},
				},
			},
		}, n)
	})

	t.Run("binary expression: range", func(t *testing.T) {
		n := MustParseModule("($a .. $b)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 10},
			},
			Statements: []Node{
				&BinaryExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 10},
					},
					Operator: Range,
					Left: &Variable{
						NodeBase: NodeBase{
							NodeSpan{1, 3},
						},
						Name: "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{
							NodeSpan{7, 9},
						},
						Name: "b",
					},
				},
			},
		}, n)
	})

	t.Run("binary expression: exclusive end  range", func(t *testing.T) {
		n := MustParseModule("($a ..< $b)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 11},
			},
			Statements: []Node{
				&BinaryExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 11},
					},
					Operator: ExclEndRange,
					Left: &Variable{
						NodeBase: NodeBase{
							NodeSpan{1, 3},
						},
						Name: "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{
							NodeSpan{8, 10},
						},
						Name: "b",
					},
				},
			},
		}, n)
	})

	t.Run("upper bound range expression", func(t *testing.T) {
		n := MustParseModule("..10")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 4},
			},
			Statements: []Node{
				&UpperBoundRangeExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
					},
					UpperBound: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{2, 4},
						},
						Raw:   "10",
						Value: 10,
					},
				},
			},
		}, n)
	})

	t.Run("function expression : no parameters, no requirements, empty body ", func(t *testing.T) {
		n := MustParseModule("fn(){}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 6},
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
					},
					Parameters: nil,
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{4, 6},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("function expression : no parameters, empty requirements, empty body ", func(t *testing.T) {
		n := MustParseModule("fn() require {} {}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 18},
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 18},
					},
					Parameters: nil,
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{16, 18},
						},
						Statements: nil,
					},
					Requirements: &Requirements{
						Object: &ObjectLiteral{
							NodeBase: NodeBase{
								NodeSpan{13, 15},
							},
							Properties: nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("function expression : single parameter, empty body ", func(t *testing.T) {
		n := MustParseModule("fn($x){}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 8},
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 8},
					},
					Parameters: []FunctionParameter{
						{
							Var: &Variable{
								NodeBase: NodeBase{
									NodeSpan{3, 5},
								},
								Name: "x",
							},
						},
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{6, 8},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("function expression : two parameters, empty body ", func(t *testing.T) {
		n := MustParseModule("fn($x,$n){}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 11},
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 11},
					},
					Parameters: []FunctionParameter{
						{
							Var: &Variable{
								NodeBase: NodeBase{
									NodeSpan{3, 5},
								},
								Name: "x",
							},
						},
						{
							Var: &Variable{
								NodeBase: NodeBase{
									NodeSpan{6, 8},
								},
								Name: "n",
							},
						},
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{9, 11},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})
	t.Run("lazy expression : '@' integer ", func(t *testing.T) {
		n := MustParseModule("@1")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 2},
			},
			Statements: []Node{
				&LazyExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 2},
					},
					Expression: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{1, 2},
						},
						Raw:   "1",
						Value: 1,
					},
				},
			},
		}, n)
	})

	t.Run("switch statement : single case", func(t *testing.T) {
		n := MustParseModule("switch 1 { 1 { } }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 18},
			},
			Statements: []Node{
				&SwitchStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 18},
					},
					Discriminant: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
						},
						Raw:   "1",
						Value: 1,
					},
					Cases: []*Case{
						{
							NodeBase: NodeBase{
								NodeSpan{11, 16},
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{11, 12},
								},
								Raw:   "1",
								Value: 1,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{13, 16},
								},
								Statements: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("switch statement : two cases", func(t *testing.T) {
		n := MustParseModule("switch 1 { 1 { } 2 { } }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 24},
			},
			Statements: []Node{
				&SwitchStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 24},
					},
					Discriminant: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
						},
						Raw:   "1",
						Value: 1,
					},
					Cases: []*Case{
						{
							NodeBase: NodeBase{
								NodeSpan{11, 16},
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{11, 12},
								},
								Raw:   "1",
								Value: 1,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{13, 16},
								},
								Statements: nil,
							},
						},
						{
							NodeBase: NodeBase{
								NodeSpan{17, 22},
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{17, 18},
								},
								Raw:   "2",
								Value: 2,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{19, 22},
								},
								Statements: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("switch statement : two value case", func(t *testing.T) {
		n := MustParseModule("switch 1 { 1, 2 { } }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 21},
			},
			Statements: []Node{
				&SwitchStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 21},
					},
					Discriminant: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
						},
						Raw:   "1",
						Value: 1,
					},
					Cases: []*Case{
						{
							NodeBase: NodeBase{
								NodeSpan{11, 19},
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{11, 12},
								},
								Raw:   "1",
								Value: 1,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{16, 19},
								},
								Statements: nil,
							},
						},
						{
							NodeBase: NodeBase{
								NodeSpan{14, 19},
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{14, 15},
								},
								Raw:   "2",
								Value: 2,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{16, 19},
								},
								Statements: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("switch statement : case is not a simple literal", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("switch 1 { $a { } }")
		})
	})

	t.Run("match statement : case is not a simple literal", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("match 1 { $a { } }")
		})
	})

	t.Run("empty single line comment", func(t *testing.T) {
		n := MustParseModule("# ")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 2},
			},
			Statements: nil,
		}, n)
	})

	t.Run("not empty single line comment", func(t *testing.T) {
		n := MustParseModule("# some text")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 11},
			},
			Statements: nil,
		}, n)
	})

	t.Run("import statement", func(t *testing.T) {
		n := MustParseModule(`import a https://example.com/a.gos "sS1pD9weZBuJdFmowNwbpi7BJ8TNftyUImj/0WQi72jY=" {} allow {}`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 94},
			},
			Statements: []Node{
				&ImportStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 94},
					},
					Identifier: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
						},
						Name: "a",
					},
					ValidationString: &StringLiteral{
						NodeBase: NodeBase{
							NodeSpan{35, 82},
						},
						Raw:   `"sS1pD9weZBuJdFmowNwbpi7BJ8TNftyUImj/0WQi72jY="`,
						Value: "sS1pD9weZBuJdFmowNwbpi7BJ8TNftyUImj/0WQi72jY=",
					},
					URL: &URLLiteral{
						NodeBase: NodeBase{
							NodeSpan{9, 34},
						},
						Value: "https://example.com/a.gos",
					},
					ArgumentObject: &ObjectLiteral{
						NodeBase: NodeBase{
							NodeSpan{83, 85},
						},
						Properties: nil,
					},
					GrantedPermissions: &ObjectLiteral{
						NodeBase: NodeBase{
							NodeSpan{92, 94},
						},
						Properties: nil,
					},
				},
			},
		}, n)
	})

	t.Run("spawn expression", func(t *testing.T) {
		n := MustParseModule(`sr nil f()`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 10},
			},
			Statements: []Node{
				&SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 10},
					},
					Globals: &NilLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 6},
						},
					},
					ExprOrVar: &Call{
						NodeBase: NodeBase{
							NodeSpan{7, 10},
						},
						Callee: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{7, 8},
							},
							Name: "f",
						},
					},
				},
			},
		}, n)
	})

	t.Run("spawn expression : embedded module", func(t *testing.T) {
		n := MustParseModule(`sr nil { require {} }`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 21},
			},
			Statements: []Node{
				&SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 21},
					},
					Globals: &NilLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 6},
						},
					},
					ExprOrVar: &EmbeddedModule{
						NodeBase: NodeBase{
							NodeSpan{7, 21},
						},
						Requirements: &Requirements{
							&ObjectLiteral{
								NodeBase: NodeBase{
									NodeSpan{17, 19},
								},
								Properties: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("spawn expression : group", func(t *testing.T) {
		n := MustParseModule(`sr group nil f()`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 16},
			},
			Statements: []Node{
				&SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 16},
					},
					GroupIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 8},
						},
						Name: "group",
					},
					Globals: &NilLiteral{
						NodeBase: NodeBase{
							NodeSpan{9, 12},
						},
					},
					ExprOrVar: &Call{
						NodeBase: NodeBase{
							NodeSpan{13, 16},
						},
						Callee: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{13, 14},
							},
							Name: "f",
						},
					},
				},
			},
		}, n)
	})
}

type User struct {
	Name   string
	secret string
}

type Named interface {
	GetName(*Context) string
}

func (user User) GetName(ctx *Context) string {
	return user.Name
}

func TestCheck(t *testing.T) {

	t.Run("object literal with two implict keys", func(t *testing.T) {
		n := MustParseModule(`{:1, :2}`)
		assert.NoError(t, Check(n.Statements[0]))
	})

	t.Run("object literal with explicit identifier keys", func(t *testing.T) {
		n := MustParseModule(`{keyOne:1, keyTwo:2}`)
		assert.NoError(t, Check(n.Statements[0]))
	})

	t.Run("object literal with duplicate keys (one implicit, the other one explicit)", func(t *testing.T) {
		n := MustParseModule(`{:1, "0": 1}`)
		assert.Error(t, Check(n.Statements[0]))

		n = MustParseModule(`{"0": 1, :1}`)
		assert.Error(t, Check(n.Statements[0]))
	})

	t.Run("object literal with duplicate keys in same multi-key definition", func(t *testing.T) {
		n := MustParseModule(`{a,a:1}`)
		assert.Error(t, Check(n.Statements[0]))
	})

	t.Run("object literal with duplicate key prop : two explicit keys", func(t *testing.T) {
		n := MustParseModule(`{"0":1, "0": 1}`)
		assert.Error(t, Check(n.Statements[0]))
	})

	t.Run("spawn expression : expression is a nil literal", func(t *testing.T) {
		n := MustParseModule(`sr {} nil`)
		assert.Error(t, Check(n.Statements[0]))
	})

	t.Run("spawn expression : expression is an integer literal", func(t *testing.T) {
		n := MustParseModule(`sr {} 1`)
		assert.Error(t, Check(n.Statements[0]))
	})

	t.Run("spawn expression : embedded module", func(t *testing.T) {
		n := MustParseModule(`sr {} {}`)
		assert.NoError(t, Check(n.Statements[0]))
	})

	t.Run("function declaration in another function declaration", func(t *testing.T) {
		n := MustParseModule(`
			fn f(){
				fn g(){

				}
			}
		`)
		assert.Error(t, Check(n))
	})

	t.Run("function declared twice", func(t *testing.T) {
		n := MustParseModule(`
			fn f(){}
			fn f(){}
		`)
		assert.Error(t, Check(n))
	})

	t.Run("function with same name in an embedded module", func(t *testing.T) {
		n := MustParseModule(`
			fn f(){}

			sr nil {
				fn f(){}
			}
		`)
		assert.NoError(t, Check(n))
	})
}

func TestRequirements(t *testing.T) {

	testCases := []struct {
		name                string
		inputModule         string
		expectedPermissions []Permission
		expectedLimitations []Limitation
	}{
		{"empty requirements", `require {}`, []Permission{}, []Limitation{}},
		{"read_any_global", `require { read: {globals: "*"} }`, []Permission{
			GlobalVarPermission{ReadPerm, "*"},
		}, []Limitation{}},
		{"create_routine", `require { create: {routines: {}} }`, []Permission{
			RoutinePermission{CreatePerm},
		}, []Limitation{}},
		{"create_routine", `require { create: {routines: {}} }`, []Permission{
			RoutinePermission{CreatePerm},
		}, []Limitation{}},
		{"read_@const_var", `
			const (
				$$URL = https://example.com/
			)
			require { 
				read: $$URL
			}
		`, []Permission{
			HttpPermission{ReadPerm, URL("https://example.com/")},
		}, []Limitation{}},
		{"limitations", `
			require { 
				limits: {
					"http/upload": 100kB/s
				}
			}
		`, []Permission{}, []Limitation{
			{Name: "http/upload", Rate: ByteRate(100_000)},
		}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mod := MustParseModule(testCase.inputModule)
			perms, limitations := mod.Requirements.Object.PermissionsLimitations(mod.GlobalConstantDeclarations, nil, nil)
			assert.EqualValues(t, testCase.expectedPermissions, perms)
			assert.EqualValues(t, testCase.expectedLimitations, limitations)
		})
	}

}

func TestEval(t *testing.T) {

	var DEFAULT_TEST_CTX = NewContext([]Permission{
		GlobalVarPermission{ReadPerm, "*"},
		GlobalVarPermission{UpdatePerm, "*"},
		GlobalVarPermission{CreatePerm, "*"},
		GlobalVarPermission{UsePerm, "*"},

		HttpPermission{ReadPerm, HTTPHostPattern("https://*")},
		RoutinePermission{CreatePerm},
	}, nil)

	t.Run("integer literal", func(t *testing.T) {
		n := MustParseModule("1")
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("string literal", func(t *testing.T) {
		n := MustParseModule(`"a"`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.EqualValues(t, "a", res)
	})

	t.Run("boolean literal", func(t *testing.T) {
		n := MustParseModule(`true`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.EqualValues(t, true, res)
	})

	t.Run("nil literal", func(t *testing.T) {
		n := MustParseModule(`nil`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.EqualValues(t, nil, res)
	})

	t.Run("absolute path literal", func(t *testing.T) {
		n := MustParseModule(`/`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.Equal(t, Path("/"), res)
	})

	t.Run("relative path literal", func(t *testing.T) {
		n := MustParseModule(`./`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.Equal(t, Path("./"), res)
	})

	t.Run("absolute path pattern literal", func(t *testing.T) {
		n := MustParseModule(`/*`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.Equal(t, PathPattern("/*"), res)
	})

	t.Run("relative path pattern literal", func(t *testing.T) {
		n := MustParseModule(`./*`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.Equal(t, PathPattern("./*"), res)
	})

	t.Run("absolute path expression", func(t *testing.T) {
		n := MustParseModule(`/home/$username$`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"username": "foo",
		}))
		assert.NoError(t, err)
		assert.Equal(t, Path("/home/foo"), res)
	})

	t.Run("HTTP host", func(t *testing.T) {
		n := MustParseModule(`https://example.com`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.Equal(t, HTTPHost("https://example.com"), res)
	})

	t.Run("HTTP host pattern", func(t *testing.T) {
		n := MustParseModule(`https://*.example.com`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.Equal(t, HTTPHostPattern("https://*.example.com"), res)
	})

	t.Run("URL expression", func(t *testing.T) {
		n := MustParseModule(`https://example.com/$path$`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"path": "/index.html",
		}))
		assert.NoError(t, err)
		assert.Equal(t, URL("https://example.com/index.html"), res)
	})

	t.Run("variable assignment", func(t *testing.T) {
		n := MustParseModule(`$a = 1; return $a`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("variable assignment (lhs: identifier literal)", func(t *testing.T) {
		n := MustParseModule(`a = 1; return $a`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("const global variable assignment", func(t *testing.T) {
		n := MustParseModule(`
			const (
				$$A = 1
			)

			require {
				update: {
					globals: "*"
				}
			}

			$$A = 2;
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("element assignment", func(t *testing.T) {
		n := MustParseModule(`$a = [0] $a[0] = 1; return $a`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{1}, res)
	})

	t.Run("member expression assignment : pre existing field", func(t *testing.T) {
		n := MustParseModule(`$a = {count:0}; $a.count = 1; return $a.count`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("member expression assignment : non existing field", func(t *testing.T) {
		n := MustParseModule(`$a = {}; $a.count = 1; return $a.count`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("rate literal", func(t *testing.T) {
		n := MustParseModule(`10kB/s`)
		res, err := Eval(n.Statements[0], NewState(DEFAULT_TEST_CTX))
		assert.NoError(t, err)
		assert.EqualValues(t, ByteRate(10_000), res)
	})

	t.Run("global constants : empty", func(t *testing.T) {
		n := MustParseModule(`
			const ()
		`)
		state := NewState(DEFAULT_TEST_CTX)
		_, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, map[string]interface{}{}, state.GlobalScope())
	})

	t.Run("global constants : single", func(t *testing.T) {
		n := MustParseModule(`
			const (
				$$a = 1
			)
		`)
		state := NewState(DEFAULT_TEST_CTX)
		_, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, map[string]interface{}{"a": 1}, state.GlobalScope())
	})

	t.Run("empty object literal", func(t *testing.T) {
		n := MustParseModule(`{}`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{}, res)
	})

	t.Run("single prop object literal", func(t *testing.T) {
		n := MustParseModule(`{a:1}`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{"a": 1}, res)
	})

	t.Run("multiprop object literal", func(t *testing.T) {
		n := MustParseModule(`{a:1,b:2}`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{"a": 1, "b": 2}, res)
	})

	t.Run("object literal with only an implicit key prop", func(t *testing.T) {
		n := MustParseModule(`{:1}`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{"0": 1, IMPLICIT_KEY_LEN_KEY: 1}, res)
	})

	t.Run("empty list literal", func(t *testing.T) {
		n := MustParseModule(`[]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{}, res)
	})

	t.Run("list literal : [integer]", func(t *testing.T) {
		n := MustParseModule(`[1]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1}, res)
	})

	t.Run("list literal : [integer,integer]", func(t *testing.T) {
		n := MustParseModule(`[1,2]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 2}, res)
	})

	t.Run("multi assignement", func(t *testing.T) {
		n := MustParseModule(`assign $a $b = [1, 2]; return [$a, $b]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 2}, res)
	})

	t.Run("if statement with true condition", func(t *testing.T) {
		n := MustParseModule(`if true { return 1 }`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("if statement with false condition", func(t *testing.T) {
		n := MustParseModule(`$a = 0; if false { $a = 1 }; return $a`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 0, res)
	})

	t.Run("if-else statement with false condition", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; if false { $a = 1 } else { $b = 1 }; return [$a, $b]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{0, 1}, res)
	})

	t.Run("for statement : empty list", func(t *testing.T) {
		n := MustParseModule(`$c = 0; for $i, $e in [] { $c = 1 }; return $c`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 0, res)
	})

	t.Run("for statement : single elem list", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 2; for $i, $e in [5] { $c1 = $i; $c2 = $e; }; return [$c1, $c2]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{0, 5}, res)
	})

	t.Run("for statement : two-elem list", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 0; for $i, $e in [5,6] { $c1 = ($c1 + $i); $c2 = ($c2 + $e); }; return [$c1, $c2]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 11}, res)
	})

	t.Run("for statement : two-elem list", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 0; for $i, $e in [5,6] { $c1 = ($c1 + $i); $c2 = ($c2 + $e); }; return [$c1, $c2]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 11}, res)
	})

	t.Run("for statement : integer range", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 0; for $i, $e in (5 .. 6) { $c1 = ($c1 + $i); $c2 = ($c2 + $e); }; return [$c1, $c2]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 11}, res)
	})

	t.Run("for <expr> statement", func(t *testing.T) {
		n := MustParseModule(`$c = 0; for (1 .. 2) { $c = ($c + 1) }; return $c`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 2, res)
	})

	t.Run("switch statement : singe case (matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; switch 0 { 0 { $a = 1 } }; return $a`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("switch statement : two cases (first matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; switch 0 { 0 { $a = 1 } 1 { $b = 1} }; return [$a,$b]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{1, 0}, res)
	})

	t.Run("switch statement : two cases (second matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; switch 1 { 0 { $a = 1 } 1 { $b = 1 } }; return [$a,$b]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{0, 1}, res)
	})

	t.Run("match statement : matchers : two cases (first matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; match / { /* { $a = 1 } /e* { $b = 1} }; return [$a,$b]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{1, 0}, res)
	})

	t.Run("match statement : matchers : two cases (second matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; match /e { /f* { $a = 1 } /e* { $b = 1} }; return [$a,$b]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{0, 1}, res)
	})

	t.Run("match statement : equality : two cases (second matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; match /e { /f* { $a = 1 } /e { $b = 1} }; return [$a,$b]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{0, 1}, res)
	})

	t.Run("match statement : seconde case is not a matcher nor value of the same type ", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; match /e { /f* { $a = 1 } 1 { $b = 1} }; return [$a,$b]`)
		state := NewState(DEFAULT_TEST_CTX)
		_, err := Eval(n, state)
		assert.Error(t, err)
	})

	t.Run("upper bound range expression : integer ", func(t *testing.T) {
		n := MustParseModule(`return ..10`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, IntRange{
			unknownStart: true,
			inclusiveEnd: true,
			Start:        0,
			End:          10,
			Step:         1,
		}, res.(reflect.Value).Interface())
	})

	t.Run("upper bound range expression : quantity", func(t *testing.T) {
		n := MustParseModule(`return ..10s`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, QuantityRange{
			unknownStart: true,
			inclusiveEnd: true,
			Start:        nil,
			End:          time.Duration(10 * time.Second),
		}, res.(reflect.Value).Interface())
	})

	t.Run("function expression : empty", func(t *testing.T) {
		n := MustParseModule(`fn(){}`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)

		assert.IsType(t, &FunctionExpression{}, res)
	})

	t.Run("function declaration", func(t *testing.T) {
		n := MustParseModule(`fn f(){}`)
		state := NewState(DEFAULT_TEST_CTX)
		_, err := Eval(n, state)
		assert.NoError(t, err)

		assert.Contains(t, state.GlobalScope(), "f")
		assert.IsType(t, &FunctionDeclaration{}, state.GlobalScope()["f"])
	})

	t.Run("call declared void function", func(t *testing.T) {
		n := MustParseModule(`fn f(){  }; return f()`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, nil, res)
	})

	t.Run("call declared non-void function", func(t *testing.T) {
		n := MustParseModule(`fn f(){ return 1 }; return f()`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("call variadic Go function : arg count < non-variadic-param-count", func(t *testing.T) {
		n := MustParseModule(`gofunc()`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context, x int, xs ...int) {},
		})
		_, err := Eval(n, state)
		assert.Error(t, err)
	})

	t.Run("call variadic Go function : arg count == non-variadic-param-count", func(t *testing.T) {
		n := MustParseModule(`gofunc(1)`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context, x int, xs ...int) int {
				return x
			},
		})
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("call variadic Go function : arg count == 1 + non-variadic-param-count", func(t *testing.T) {
		n := MustParseModule(`gofunc(1 2)`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context, x int, xs ...int) int {
				return x + xs[0]
			},
		})
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, 3, res)
	})

	t.Run("call Go function with a mix of non-Go & Go values", func(t *testing.T) {
		n := MustParseModule(`gofunc 1 getval()`)
		called := false
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"getval": func(ctx *Context) url.URL {
				return url.URL{}
			},
			"gofunc": func(ctx *Context, x int, u url.URL) {
				called = true
			},
		})
		_, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("call Go function with an Object convertible to the expected struct argument", func(t *testing.T) {
		n := MustParseModule(`gofunc({Name: "foo"})`)
		called := false
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context, user User) {
				called = true
				assert.Equal(t, "foo", user.Name)
			},
		})
		_, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("call Go function with an Object not convertible to the expected struct argument", func(t *testing.T) {
		n := MustParseModule(`gofunc({X: "foo"})`)
		called := false
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context, user User) {
				called = true
				assert.Equal(t, "foo", user.Name)
			},
		})
		_, err := Eval(n.Statements[0], state)
		assert.False(t, called)
		assert.Error(t, err)
	})

	t.Run("call Go function with an Object not convertible to the expected struct argument", func(t *testing.T) {
		n := MustParseModule(`gofunc({Name: 1})`)
		called := false
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(user User) {
				called = true
				assert.Equal(t, "foo", user.Name)
			},
		})
		_, err := Eval(n.Statements[0], state)
		assert.False(t, called)
		assert.Error(t, err)
	})

	t.Run("call Go function : external values should be unwrapped", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr {gofunc: $$gofunc, x: {a: 1}} {
				return gofunc($$x)
			}

			$rt.WaitResult()
		`)
		called := false
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context, obj Object) {
				called = true
				assert.Equal(t, Object{"a": 1}, obj)
			},
		})
		_, err := Eval(n, state)
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("(must) call Go function with two results", func(t *testing.T) {
		n := MustParseModule(`return gofunc()!`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context) (int, error) {
				return 3, nil
			},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 3, res)
	})

	t.Run("call declared non-void function : return in if", func(t *testing.T) {
		n := MustParseModule(`fn f(){ if true { return 1 } }; return f()`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("call struct method", func(t *testing.T) {
		n := MustParseModule(`return $$user.GetName()`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"user": User{"Foo", ""},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, "Foo", res)
	})

	t.Run("call interface method", func(t *testing.T) {
		n := MustParseModule(`return $$named.GetName()`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"named": Named(User{"Foo", ""}),
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, "Foo", res)
	})

	t.Run("call non-Go external func : no parameters, no return value", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr nil { return fn(){} }

			$f = $rt.WaitResult()!
			return $f()
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, nil, res)
	})

	t.Run("call non-Go external func : no parameters, returns an integer", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr nil { return fn(){  return 1 } }

			$f = $rt.WaitResult()!
			return $f()
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("call non-Go external func : no parameters, returns an object", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr nil { return fn(){  return {} } }

			$f = $rt.WaitResult()!
			return $f()
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.IsType(t, ExternalValue{}, res)
		assert.IsType(t, Object{}, res.(ExternalValue).value)
	})

	t.Run("member expression : <variable> <propname>", func(t *testing.T) {
		n := MustParseModule(`$a = {v: 1}; return $a.v`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("member expression : '(' <object literal> ')' <propname>", func(t *testing.T) {
		n := MustParseModule(`return ({a:1}).a`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("member expression : unexported field", func(t *testing.T) {
		n := MustParseModule(`return $$val.secret`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"val": User{Name: "Foo", secret: "secret"},
		})
		res, err := Eval(n, state)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("index expression : <variable> '[' 0 ']", func(t *testing.T) {
		n := MustParseModule(`$a = ["a"]; return $a[0]`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, "a", res)
	})

	t.Run("key list expression : empty", func(t *testing.T) {
		n := MustParseModule(`return .{}`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, KeyList{}, res)
	})

	t.Run("key list expression : single element", func(t *testing.T) {
		n := MustParseModule(`return .{name}`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, KeyList{"name"}, res)
	})

	t.Run("lazy expression : @ <integer>", func(t *testing.T) {
		n := MustParseModule(`@1`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, &LazyExpression{
			NodeBase: NodeBase{NodeSpan{0, 2}},
			Expression: &IntLiteral{
				NodeBase: NodeBase{NodeSpan{1, 2}},
				Raw:      "1",
				Value:    1,
			},
		}, res)
	})

	t.Run("import statement : no globals, no required permissions", func(t *testing.T) {
		n := MustParseModule(strings.ReplaceAll(`
			import importname https://modules.com/return_1.gos "<hash>" {} allow {}
			return $$importname
		`, "<hash>", RETURN_1_MODULE_HASH))
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("import statement : imported module returns $$a", func(t *testing.T) {
		n := MustParseModule(strings.ReplaceAll(`
			import importname https://modules.com/return_global_a.gos "<hash>" {a: 1} allow {read: {globals: "a"}}
			return $$importname
		`, "<hash>", RETURN_GLOBAL_A_MODULE_HASH))
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("spawn expression : no globals, empty embedded module", func(t *testing.T) {
		n := MustParseModule(`
			sr nil { }
		`)
		state := NewState(DEFAULT_TEST_CTX)
		_, err := Eval(n, state)
		assert.NoError(t, err)
	})

	t.Run("spawn expression : no globals, embedded module returns a simple value", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr nil { 
				return 1
			}

			return $rt.WaitResult()!
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("spawn expression : no globals, embedded module returns a simple value", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr nil { 
				return { }
			}

			return $rt.WaitResult()!
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.IsType(t, ExternalValue{}, res)
		assert.Equal(t, Object{}, res.(ExternalValue).value)
	})

	t.Run("spawn expression : no globals, allow <runtime requirements>", func(t *testing.T) {
		n := MustParseModule(`
			$$URL = https://example.com/
			$rt = sr nil { 

			} allow { 
				read: $$URL
			}

			$rt.WaitResult()!
		`)
		state := NewState(DEFAULT_TEST_CTX)
		_, err := Eval(n, state)
		assert.NoError(t, err)
	})

	t.Run("spawn expression : no globals, group (used once)", func(t *testing.T) {
		n := MustParseModule(`
			sr group nil { }

			return $group
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.IsType(t, reflect.Value{}, res)
		assert.IsType(t, &RoutineGroup{}, res.(reflect.Value).Interface())

		group := res.(reflect.Value).Interface().(*RoutineGroup)
		assert.Len(t, group.routines, 1)
	})

	t.Run("spawn expression : no globals, group (used twice)", func(t *testing.T) {
		n := MustParseModule(`
			sr group nil { }
			sr group nil { }

			return $group
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.IsType(t, reflect.Value{}, res)
		assert.IsType(t, &RoutineGroup{}, res.(reflect.Value).Interface())

		group := res.(reflect.Value).Interface().(*RoutineGroup)
		assert.Len(t, group.routines, 2)
	})

	t.Run("spawn expression : call Go func", func(t *testing.T) {
		called := false
		n := MustParseModule(`
			$rt = sr group nil gofunc()

			return $rt.WaitResult()!
		`)
		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context) int {
				called = true
				return 2
			},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, 2, res)
	})

	t.Run("external value : object : get property ", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr nil {
				return {x: 1}
			}

			$res = $rt.WaitResult()!
			return $res.x
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("external value : object : get object property ", func(t *testing.T) {
		n := MustParseModule(`
			$rt = sr nil { 
				return {x: {}}
			}

			$res = $rt.WaitResult()!
			return $res.x
		`)
		state := NewState(DEFAULT_TEST_CTX)
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.IsType(t, ExternalValue{}, res)
		assert.Equal(t, Object{}, res.(ExternalValue).value)
	})

	t.Run("an value passed to a routine and then returned by it should not be wrapped", func(t *testing.T) {
		called := false

		n := MustParseModule(`
			$rt = sr {gofunc: $$gofunc} {
				return $$gofunc
			}

			$f = $rt.WaitResult()!
			return $f()
		`)

		state := NewState(DEFAULT_TEST_CTX, map[string]interface{}{
			"gofunc": func(ctx *Context) int {
				called = true

				if ctx != DEFAULT_TEST_CTX {
					t.Fatal("the context should be the main one")
				}
				return 0
			},
		})
		_, err := Eval(n, state)
		assert.True(t, called)
		assert.NoError(t, err)
	})

}

func TestHttpPermission(t *testing.T) {

	ENTITIES := List{
		URL("https://localhost:443/"),
		HTTPHost("https://localhost:443"),
		HTTPHostPattern("https://*"),
	}

	for kind := ReadPerm; kind <= ProvidePerm; kind++ {
		for _, entity := range ENTITIES {
			t.Run(kind.String()+"_"+fmt.Sprint(entity)+"_includes_itself", func(t *testing.T) {
				perm := HttpPermission{Kind_: kind, Entity: entity}
				assert.True(t, perm.Includes(perm))
			})
		}
	}

	for kind := ReadPerm; kind <= ProvidePerm; kind++ {
		for i, entity := range ENTITIES {
			for _, prevEntity := range ENTITIES[:i] {
				t.Run(fmt.Sprintf("%s_%s_includes_%s", kind, entity, prevEntity), func(t *testing.T) {
					perm := HttpPermission{Kind_: kind, Entity: entity}
					otherPerm := HttpPermission{Kind_: kind, Entity: prevEntity}

					assert.True(t, perm.Includes(otherPerm))
				})
			}
		}
	}
}

func TestCommandPermission(t *testing.T) {
	permNoSub := CommandPermission{CommandName: "mycmd"}
	assert.True(t, permNoSub.Includes(permNoSub))

	otherPermNoSub := CommandPermission{CommandName: "mycmd2"}
	assert.False(t, otherPermNoSub.Includes(permNoSub))
	assert.False(t, permNoSub.Includes(otherPermNoSub))

	permSub1a := CommandPermission{CommandName: "mycmd", SubcommandNameChain: []string{"a"}}
	assert.True(t, permSub1a.Includes(permSub1a))
	assert.False(t, permNoSub.Includes(permSub1a))
	assert.False(t, permSub1a.Includes(permNoSub))

	permSub1b := CommandPermission{CommandName: "mycmd", SubcommandNameChain: []string{"b"}}
	assert.False(t, permSub1b.Includes(permSub1a))
	assert.False(t, permSub1a.Includes(permSub1b))
}

func TestFilesystemPermission(t *testing.T) {
	ENTITIES := List{
		Path("./"),
		PathPattern("./*.go"),
	}

	for kind := ReadPerm; kind <= ProvidePerm; kind++ {
		for _, entity := range ENTITIES {
			t.Run(kind.String()+"_"+fmt.Sprint(entity), func(t *testing.T) {
				perm := FilesystemPermission{Kind_: kind, Entity: entity}
				assert.True(t, perm.Includes(perm))
			})
		}
	}
}

func TestStackPermission(t *testing.T) {
	perm1 := StackPermission{maxHeight: 1}
	assert.True(t, perm1.Includes(perm1))

	perm2 := StackPermission{maxHeight: 2}
	assert.True(t, perm2.Includes(perm2))
	assert.True(t, perm2.Includes(perm1))
	assert.False(t, perm1.Includes(perm2))
}

func TestSpawnRoutine(t *testing.T) {

	t.Run("spawning a routine without the required permission should fail", func(t *testing.T) {
		state := NewState(nil)
		mod := MustParseModule("")
		globals := map[string]interface{}{}

		routine, err := spawnRoutine(state, globals, mod, nil)
		assert.Nil(t, routine)
		assert.Error(t, err)
	})

	t.Run("a routine should have access to globals passed to it", func(t *testing.T) {
		state := NewState(NewContext([]Permission{
			RoutinePermission{CreatePerm},
		}, nil))
		mod := MustParseModule(`
			return $$x
		`)
		globals := map[string]interface{}{
			"x": 1,
		}

		routine, err := spawnRoutine(state, globals, mod, nil)
		assert.NoError(t, err)

		res, err := routine.WaitResult(nil)
		assert.NoError(t, err)
		assert.Equal(t, res, 1)
	})

	t.Run("the result of a routine should be an ExternalValue if it is not simple", func(t *testing.T) {
		state := NewState(NewContext([]Permission{
			RoutinePermission{CreatePerm},
		}, nil))
		mod := MustParseModule(`
			return {a: 1}
		`)
		globals := map[string]interface{}{}

		routine, err := spawnRoutine(state, globals, mod, nil)
		assert.NoError(t, err)

		res, err := routine.WaitResult(nil)
		assert.NoError(t, err)
		assert.EqualValues(t, ExternalValue{
			state: routine.state,
			value: Object{"a": 1},
		}, res)
	})
}

func TestTraverse(t *testing.T) {

	t.Run("integer", func(t *testing.T) {
		called := false

		err := Traverse(1, func(v interface{}) (TraversalAction, error) {
			called = true
			return Continue, nil
		}, TraversalConfiguration{})

		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("empty object", func(t *testing.T) {
		called := false

		err := Traverse(Object{}, func(v interface{}) (TraversalAction, error) {
			called = true
			return Continue, nil
		}, TraversalConfiguration{})

		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("object with an integer property : max depth = 0", func(t *testing.T) {
		callCount := 0

		err := Traverse(Object{"n": 1}, func(v interface{}) (TraversalAction, error) {
			callCount++
			return Continue, nil
		}, TraversalConfiguration{MaxDepth: 0})

		assert.NoError(t, err)
		assert.Equal(t, 1, callCount)
	})

	t.Run("object with an integer property : max depth = 1", func(t *testing.T) {
		callCount := 0

		err := Traverse(Object{"n": 1}, func(v interface{}) (TraversalAction, error) {
			callCount++
			return Continue, nil
		}, TraversalConfiguration{MaxDepth: 1})

		assert.NoError(t, err)
		assert.Equal(t, 2, callCount)
	})

	t.Run("object with a reference to itself", func(t *testing.T) {
		callCount := 0

		obj := Object{}
		obj["self"] = obj

		err := Traverse(obj, func(v interface{}) (TraversalAction, error) {
			callCount++
			return Continue, nil
		}, TraversalConfiguration{MaxDepth: 10})

		assert.NoError(t, err)
		assert.Equal(t, 1, callCount)
	})

	t.Run("list with a reference to itself", func(t *testing.T) {
		callCount := 0

		list := List{}
		list = append(list, nil)
		list[0] = list

		err := Traverse(list, func(v interface{}) (TraversalAction, error) {
			callCount++
			return Continue, nil
		}, TraversalConfiguration{MaxDepth: 10})

		assert.NoError(t, err)
		assert.Equal(t, 1, callCount)

		t.Run("pruning", func(t *testing.T) {
			callCount := 0

			v := List{
				Object{
					"v": 1,
				},
				Object{
					"v": 2,
				},
			}
			err := Traverse(v, func(v interface{}) (TraversalAction, error) {
				callCount++
				if obj, ok := v.(Object); ok && obj["v"] == 1 {
					return Prune, nil
				}
				return Continue, nil
			}, TraversalConfiguration{MaxDepth: 10})

			assert.NoError(t, err)
			assert.Equal(t, 4, callCount)
		})

		t.Run("stop", func(t *testing.T) {
			callCount := 0

			v := List{
				Object{
					"v": 1,
				},
				Object{
					"v": 2,
				},
			}
			err := Traverse(v, func(v interface{}) (TraversalAction, error) {
				callCount++
				if obj, ok := v.(Object); ok && obj["v"] == 1 {
					return StopTraversal, nil
				}
				return Continue, nil
			}, TraversalConfiguration{MaxDepth: 10})

			assert.NoError(t, err)
			assert.Equal(t, 2, callCount)
		})
	})
}

func TestLimiters(t *testing.T) {
	ctx := NewContext(nil, []Limitation{
		{Name: "fs/read", Rate: 1_000},
	})

	start := time.Now()
	expectedTime := start.Add(time.Second)

	//should not cause a wait
	ctx.Take("fs/read", 1_000)
	assert.WithinDuration(t, start, time.Now(), time.Millisecond)

	//should cause a wait
	ctx.Take("fs/read", 1_000)
	assert.WithinDuration(t, expectedTime, time.Now(), 200*time.Millisecond)
}
