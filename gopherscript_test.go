package gopherscript

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func parseEval(t *testing.T, s string) interface{} {
	mod, err := ParseAndCheckModule(s, "")
	assert.NoError(t, err)

	res, err := Eval(mod, NewState(NewDefaultTestContext()))
	assert.NoError(t, err)
	return res
}

func TestWalk(t *testing.T) {

	t.Run("prune", func(t *testing.T) {
		mod := MustParseModule("1")
		Walk(mod, func(node, parent, scopeNode Node, n4 []Node) (error, TraversalAction) {
			switch node.(type) {
			case *Module:
				return nil, Prune
			default:
				t.Fatal("the traversal should get pruned on the Module")
			}
			return nil, Continue
		})
	})

	t.Run("stop", func(t *testing.T) {
		mod := MustParseModule("1 2")
		Walk(mod, func(node, parent, scopeNode Node, n4 []Node) (error, TraversalAction) {
			switch n := node.(type) {
			case *IntLiteral:
				if n.Value == 2 {
					t.Fatal("the traversal should have stopped")
				}
				return nil, StopTraversal
			}
			return nil, Continue
		})
	})
}
func TestMustParseModule(t *testing.T) {

	t.Run("empty module", func(t *testing.T) {
		n := MustParseModule("")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 0}, nil, nil},
		}, n)
	})

	t.Run("module : comment start with missing space", func(t *testing.T) {
		n, err := ParseModule("#", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 1},
				nil, nil,
			},
			Statements: []Node{
				&UnknownNode{
					NodeBase: NodeBase{
						Span: NodeSpan{0, 1},
						Err: &ParsingError{
							"",
							1,
							0,
							UnspecifiedCategory,
							nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("empty module with empty requirements", func(t *testing.T) {
		n := MustParseModule("require {}")
		assert.EqualValues(t, &Module{
			NodeBase:   NodeBase{NodeSpan{0, 10}, nil, nil},
			Statements: nil,
			Requirements: &Requirements{
				ValuelessTokens: []Token{
					{REQUIRE_KEYWORD, NodeSpan{0, 7}},
				},
				Object: &ObjectLiteral{
					NodeBase:   NodeBase{NodeSpan{8, 10}, nil, nil},
					Properties: nil,
				},
			},
		}, n)
	})

	t.Run("empty const declarations", func(t *testing.T) {
		n := MustParseModule("const ()")
		assert.EqualValues(t, &Module{
			NodeBase:     NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements:   nil,
			Requirements: nil,
			GlobalConstantDeclarations: &GlobalConstantDeclarations{
				NodeBase: NodeBase{
					NodeSpan{0, 8},
					nil,
					[]Token{{CONST_KEYWORD, NodeSpan{0, 5}}},
				},
				Declarations: nil,
			},
		}, n)
	})

	t.Run("const declarations : (single) valid lhs & rhs", func(t *testing.T) {
		n := MustParseModule("const ( a = 1 )")
		assert.EqualValues(t, &Module{
			NodeBase:     NodeBase{NodeSpan{0, 15}, nil, nil},
			Statements:   nil,
			Requirements: nil,
			GlobalConstantDeclarations: &GlobalConstantDeclarations{
				NodeBase: NodeBase{
					NodeSpan{0, 15},
					nil,
					[]Token{{CONST_KEYWORD, NodeSpan{0, 5}}},
				},
				Declarations: []*GlobalConstantDeclaration{
					{
						NodeBase: NodeBase{
							NodeSpan{8, 13},
							nil,
							nil,
						},
						Left: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{8, 9},
								nil,
								nil,
							},
							Name: "a",
						},
						Right: &IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{12, 13},
								nil,
								nil,
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
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&Variable{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
					Name:     "a",
				},
			},
		}, n)
	})

	t.Run("module with two variables on the same line", func(t *testing.T) {
		n := MustParseModule("$a;$b")
		assert.EqualValues(t, &Module{

			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&Variable{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
					Name:     "a",
				},
				&Variable{
					NodeBase: NodeBase{NodeSpan{3, 5}, nil, nil},
					Name:     "b",
				},
			}}, n)
	})

	t.Run("boolean literal : true", func(t *testing.T) {
		n := MustParseModule("true")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&BooleanLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Value:    true,
				},
			},
		}, n)
	})

	t.Run("boolean literal : false", func(t *testing.T) {
		n := MustParseModule("false")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&BooleanLiteral{
					NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
					Value:    false,
				},
			},
		}, n)
	})

	t.Run("flag literal : single dash / single letter", func(t *testing.T) {
		n := MustParseModule("-a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&FlagLiteral{
					NodeBase:   NodeBase{NodeSpan{0, 2}, nil, nil},
					Name:       "a",
					SingleDash: true,
				},
			},
		}, n)
	})

	t.Run("flag literal : double dash", func(t *testing.T) {
		n := MustParseModule("--abc")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&FlagLiteral{
					NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
					Name:     "abc",
				},
			},
		}, n)
	})

	t.Run("flag literal : single dash not followed by characters", func(t *testing.T) {
		n, err := ParseModule("-", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 1}, nil, nil},
			Statements: []Node{
				&FlagLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 1},
						&ParsingError{
							"'-' should be followed an option name",
							1,
							0,
							KnownType,
							(*FlagLiteral)(nil),
						},
						nil,
					},
					Name:       "",
					SingleDash: true,
				},
			},
		}, n)
	})

	t.Run("flag literal : two dashes not followed by characters", func(t *testing.T) {
		n, err := ParseModule("--", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&FlagLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 2},
						&ParsingError{
							"'--' should be followed an option name",
							2,
							0,
							KnownType,
							(*FlagLiteral)(nil),
						},
						nil,
					},
					Name:       "",
					SingleDash: false,
				},
			},
		}, n)
	})

	t.Run("option expression : ok", func(t *testing.T) {
		n := MustParseModule(`--name="foo"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 12}, nil, nil},
			Statements: []Node{
				&OptionExpression{
					NodeBase: NodeBase{NodeSpan{0, 12}, nil, nil},
					Name:     "name",
					Value: &StringLiteral{
						NodeBase: NodeBase{NodeSpan{7, 12}, nil, nil},
						Raw:      `"foo"`,
						Value:    "foo",
					},
					SingleDash: false,
				},
			},
		}, n)
	})

	t.Run("option expression : unterminated", func(t *testing.T) {
		n, err := ParseModule(`--name=`, "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&OptionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 7},
						&ParsingError{
							Message:        "unterminated option expression, '=' should be followed by an expression",
							Index:          7,
							NodeStartIndex: 0,
							NodeCategory:   KnownType,
							NodeType:       (*OptionExpression)(nil),
						},
						nil,
					},
					Name:       "name",
					SingleDash: false,
				},
			},
		}, n)
	})

	t.Run("absolute path literal : /", func(t *testing.T) {
		n := MustParseModule("/")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 1}, nil, nil},
			Statements: []Node{
				&AbsolutePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 1}, nil, nil},
					Value:    "/",
				},
			},
		}, n)
	})

	t.Run("absolute path literal : /a", func(t *testing.T) {
		n := MustParseModule("/a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&AbsolutePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
					Value:    "/a",
				},
			},
		}, n)
	})

	t.Run("relative path literal : ./", func(t *testing.T) {
		n := MustParseModule("./")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&RelativePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
					Value:    "./",
				},
			},
		}, n)
	})

	t.Run("relative path literal : ./a", func(t *testing.T) {
		n := MustParseModule("./a")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&RelativePathLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
					Value:    "./a",
				},
			},
		}, n)
	})

	t.Run("relative path literal in list : [./]", func(t *testing.T) {
		n := MustParseModule("[./]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Elements: []Node{
						&RelativePathLiteral{
							NodeBase: NodeBase{NodeSpan{1, 3}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&AbsolutePathPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
					Value:    "/a*",
				},
			},
		}, n)
	})

	t.Run("absolute path pattern literal ending with /... ", func(t *testing.T) {
		n := MustParseModule("/a/...")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&AbsolutePathPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
					Value:    "/a/...",
				},
			},
		}, n)
	})

	t.Run("absolute path pattern literal : /... ", func(t *testing.T) {
		n := MustParseModule("/...")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&AbsolutePathPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Value:    "/...",
				},
			},
		}, n)
	})

	t.Run("named-segment path pattern literal  ", func(t *testing.T) {
		n := MustParseModule("%/home/$username$")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
			Statements: []Node{
				&NamedSegmentPathPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
					Slices: []Node{
						&PathSlice{
							NodeBase: NodeBase{
								NodeSpan{1, 7},
								nil,
								nil,
							},
							Value: "/home/",
						},
						&Variable{
							NodeBase: NodeBase{
								NodeSpan{7, 16},
								nil,
								nil,
							},
							Name: "username",
						},
					},
				},
			},
		}, n)
	})

	t.Run("invalid named-segment path pattern literals", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("%/home/e$username$")
		})
		assert.Panics(t, func() {
			MustParseModule("%/home/$username$e")
		})
		assert.Panics(t, func() {
			MustParseModule("%/home/e$username$e")
		})
	})

	t.Run("absolute path pattern literal containg /... in the middle", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("/a/.../ee")
		})
	})

	t.Run("absolute path expression : single trailing interpolation (variable)", func(t *testing.T) {
		n := MustParseModule("/home/$username$")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 16}, nil, nil},
			Statements: []Node{
				&AbsolutePathExpression{
					NodeBase: NodeBase{NodeSpan{0, 16}, nil, nil},
					Slices: []Node{
						&PathSlice{
							NodeBase: NodeBase{
								NodeSpan{0, 6},
								nil,
								nil,
							},
							Value: "/home/",
						},
						&Variable{
							NodeBase: NodeBase{
								NodeSpan{6, 15},
								nil,
								nil,
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
			NodeBase: NodeBase{NodeSpan{0, 25}, nil, nil},
			Statements: []Node{
				&AbsolutePathExpression{
					NodeBase: NodeBase{NodeSpan{0, 25}, nil, nil},
					Slices: []Node{
						&PathSlice{
							NodeBase: NodeBase{
								NodeSpan{0, 6},
								nil,
								nil,
							},
							Value: "/home/",
						},
						&Variable{
							NodeBase: NodeBase{
								NodeSpan{6, 15},
								nil,
								nil,
							},
							Name: "username",
						},
						&PathSlice{
							NodeBase: NodeBase{
								NodeSpan{16, 25},
								nil,
								nil,
							},
							Value: "/projects",
						},
					},
				},
			},
		}, n)
	})

	t.Run("regex literal : empty", func(t *testing.T) {
		n := MustParseModule(`%""`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&RegularExpressionLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
					Raw:      `""`,
					Value:    "",
				},
			},
		}, n)
	})

	t.Run("regex literal : not empty", func(t *testing.T) {
		n := MustParseModule(`%"a+"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&RegularExpressionLiteral{
					NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
					Raw:      `"a+"`,
					Value:    "a+",
				},
			},
		}, n)
	})

	t.Run("nil literal", func(t *testing.T) {
		n := MustParseModule("nil")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&NilLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <single letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{3, 4}, nil, nil},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <two-letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.bc")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{3, 5}, nil, nil},
						Name:     "bc",
					},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <propname> '.' <single-letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.b.c")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
					Left: &MemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
						Left: &Variable{
							NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
							Name:     "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{3, 4}, nil, nil},
							Name:     "b",
						},
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{5, 6}, nil, nil},
						Name:     "c",
					},
				},
			},
		}, n)
	})

	t.Run("member expression : variable '.' <propname> '.' <two-letter propname> ", func(t *testing.T) {
		n := MustParseModule("$a.b.cd")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
					Left: &MemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
						Left: &Variable{
							NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
							Name:     "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{3, 4}, nil, nil},
							Name:     "b",
						},
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{5, 7}, nil, nil},
						Name:     "cd",
					},
				},
			},
		}, n)
	})

	t.Run("extraction expression : object is a variable", func(t *testing.T) {
		n := MustParseModule("$a.{name}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
			Statements: []Node{
				&ExtractionExpression{
					NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
					Object: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					Keys: &KeyListExpression{
						NodeBase: NodeBase{NodeSpan{2, 9}, nil, nil},
						Keys: []*IdentifierLiteral{
							{
								NodeBase: NodeBase{NodeSpan{4, 8}, nil, nil},
								Name:     "name",
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("parenthesized expression", func(t *testing.T) {
		n := MustParseModule("($a)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&Variable{
					NodeBase: NodeBase{NodeSpan{1, 3}, nil, nil},
					Name:     "a",
				},
			},
		}, n)
	})

	t.Run("member of a parenthesized expression", func(t *testing.T) {
		n := MustParseModule("($a).name")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
			Statements: []Node{
				&MemberExpression{
					NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{1, 3}, nil, nil},
						Name:     "a",
					},
					PropertyName: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{5, 9}, nil, nil},
						Name:     "name",
					},
				},
			},
		}, n)
	})

	t.Run("index expression : variable '[' <integer literal> '] ", func(t *testing.T) {
		n := MustParseModule("$a[0]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&IndexExpression{
					NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
					Indexed: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					Index: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{3, 4}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&IndexExpression{
					NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
					Indexed: &MemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
						Left: &Variable{
							NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
							Name:     "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{3, 4}, nil, nil},
							Name:     "b",
						},
					},
					Index: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{5, 6}, nil, nil},
						Raw:      "0",
						Value:    0,
					},
				},
			},
		}, n)
	})

	t.Run("index expression : unterminated : variable '[' ", func(t *testing.T) {
		n, err := ParseModule("$a[", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 3},
				nil,
				nil,
			},
			Statements: []Node{
				&InvalidMemberLike{
					NodeBase: NodeBase{
						NodeSpan{0, 3},
						&ParsingError{
							"unterminated member/index expression",
							3,
							0,
							UnspecifiedCategory,
							nil,
						},
						nil,
					},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
				},
			},
		}, n)
	})

	t.Run("slice expression : variable '[' <integer literal> ':' ] ", func(t *testing.T) {
		n := MustParseModule("$a[0:]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&SliceExpression{
					NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
					Indexed: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					StartIndex: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{3, 4}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&SliceExpression{
					NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
					Indexed: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					EndIndex: &IntLiteral{
						NodeBase: NodeBase{NodeSpan{4, 5}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&KeyListExpression{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
					Keys:     nil,
				},
			},
		}, n)
	})

	t.Run("key list expression : one key", func(t *testing.T) {
		n := MustParseModule(".{name}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&KeyListExpression{
					NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
					Keys: []*IdentifierLiteral{
						{
							NodeBase: NodeBase{
								NodeSpan{2, 6},
								nil,
								nil,
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
			NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
			Statements: []Node{
				&KeyListExpression{
					NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
					Keys: []*IdentifierLiteral{
						{
							NodeBase: NodeBase{
								NodeSpan{2, 6},
								nil,
								nil,
							},
							Name: "name",
						},
						{
							NodeBase: NodeBase{
								NodeSpan{7, 10},
								nil,
								nil,
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
			NodeBase: NodeBase{NodeSpan{0, 20}, nil, nil},
			Statements: []Node{
				&URLLiteral{
					NodeBase: NodeBase{NodeSpan{0, 20}, nil, nil},
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

	t.Run("URL literal : empty query", func(t *testing.T) {
		n := MustParseModule(`https://example.com/?`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 21}, nil, nil},
			Statements: []Node{
				&URLLiteral{
					NodeBase: NodeBase{NodeSpan{0, 21}, nil, nil},
					Value:    "https://example.com/?",
				},
			},
		}, n)
	})

	t.Run("URL literal : not empty query", func(t *testing.T) {
		n := MustParseModule(`https://example.com/?a=1`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 24}, nil, nil},
			Statements: []Node{
				&URLLiteral{
					NodeBase: NodeBase{NodeSpan{0, 24}, nil, nil},
					Value:    "https://example.com/?a=1",
				},
			},
		}, n)
	})

	t.Run("URL pattern literal : prefix pattern, root", func(t *testing.T) {
		n := MustParseModule(`https://example.com/...`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 23}, nil, nil},
			Statements: []Node{
				&URLPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 23}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 19}, nil, nil},
			Statements: []Node{
				&HTTPHostLiteral{
					NodeBase: NodeBase{NodeSpan{0, 19}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
			Statements: []Node{
				&HTTPHostPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
					Value:    "https://*",
				},
			},
		}, n)
	})

	t.Run("HTTP host pattern : https://*:443", func(t *testing.T) {
		n := MustParseModule(`https://*:443`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 13}, nil, nil},
			Statements: []Node{
				&HTTPHostPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 13}, nil, nil},
					Value:    "https://*:443",
				},
			},
		}, n)
	})

	t.Run("HTTP host pattern : https://*.<tld>", func(t *testing.T) {
		n := MustParseModule(`https://*.com`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 13}, nil, nil},
			Statements: []Node{
				&HTTPHostPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 13}, nil, nil},
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

	t.Run("URL expression : no query, single trailing path interpolation", func(t *testing.T) {
		n := MustParseModule(`https://example.com/$path$`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 26}, nil, nil},
			Statements: []Node{
				&URLExpression{
					NodeBase: NodeBase{NodeSpan{0, 26}, nil, nil},
					Raw:      "https://example.com/$path$",
					HostPart: &HTTPHostLiteral{
						NodeBase: NodeBase{NodeSpan{0, 19}, nil, nil},
						Value:    "https://example.com",
					},
					Path: &AbsolutePathExpression{
						NodeBase: NodeBase{NodeSpan{19, 26}, nil, nil},
						Slices: []Node{
							&PathSlice{
								NodeBase: NodeBase{
									NodeSpan{19, 20},
									nil,
									nil,
								},
								Value: "/",
							},
							&Variable{
								NodeBase: NodeBase{
									NodeSpan{20, 25},
									nil,
									nil,
								},
								Name: "path",
							},
						},
					},
					QueryParams: []Node{},
				},
			},
		}, n)
	})

	t.Run("URL expression : no path interpolation, single trailing query interpolation", func(t *testing.T) {
		n := MustParseModule(`https://example.com/?v=$x$`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 26}, nil, nil},
			Statements: []Node{
				&URLExpression{
					NodeBase: NodeBase{NodeSpan{0, 26}, nil, nil},
					Raw:      "https://example.com/?v=$x$",
					HostPart: &HTTPHostLiteral{
						NodeBase: NodeBase{NodeSpan{0, 19}, nil, nil},
						Value:    "https://example.com",
					},
					Path: &AbsolutePathExpression{
						NodeBase: NodeBase{NodeSpan{19, 20}, nil, nil},
						Slices: []Node{
							&PathSlice{
								NodeBase: NodeBase{
									NodeSpan{19, 20},
									nil,
									nil,
								},
								Value: "/",
							},
						},
					},
					QueryParams: []Node{
						&URLQueryParameter{
							NodeBase: NodeBase{
								NodeSpan{21, 26},
								nil,
								nil,
							},
							Name: "v",
							Value: []Node{
								&URLQueryParameterSlice{
									NodeBase: NodeBase{
										NodeSpan{23, 23},
										nil,
										nil,
									},
									Value: "",
								},
								&Variable{
									NodeBase: NodeBase{
										NodeSpan{23, 26},
										nil,
										nil,
									},
									Name: "x",
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("URL expression : no path interpolation, two query interpolations", func(t *testing.T) {
		n := MustParseModule(`https://example.com/?v=$x$&w=$y$`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 32}, nil, nil},
			Statements: []Node{
				&URLExpression{
					NodeBase: NodeBase{NodeSpan{0, 32}, nil, nil},
					Raw:      "https://example.com/?v=$x$&w=$y$",
					HostPart: &HTTPHostLiteral{
						NodeBase: NodeBase{NodeSpan{0, 19}, nil, nil},
						Value:    "https://example.com",
					},
					Path: &AbsolutePathExpression{
						NodeBase: NodeBase{NodeSpan{19, 20}, nil, nil},
						Slices: []Node{
							&PathSlice{
								NodeBase: NodeBase{
									NodeSpan{19, 20},
									nil,
									nil,
								},
								Value: "/",
							},
						},
					},
					QueryParams: []Node{
						&URLQueryParameter{
							NodeBase: NodeBase{
								NodeSpan{21, 26},
								nil,
								nil,
							},
							Name: "v",
							Value: []Node{
								&URLQueryParameterSlice{
									NodeBase: NodeBase{
										NodeSpan{23, 23},
										nil,
										nil,
									},
									Value: "",
								},
								&Variable{
									NodeBase: NodeBase{
										NodeSpan{23, 26},
										nil,
										nil,
									},
									Name: "x",
								},
							},
						},
						&URLQueryParameter{
							NodeBase: NodeBase{
								NodeSpan{27, 32},
								nil,
								nil,
							},
							Name: "w",
							Value: []Node{
								&URLQueryParameterSlice{
									NodeBase: NodeBase{
										NodeSpan{29, 29},
										nil,
										nil,
									},
									Value: "",
								},
								&Variable{
									NodeBase: NodeBase{
										NodeSpan{29, 32},
										nil,
										nil,
									},
									Name: "y",
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("invalid host alias stuff", func(t *testing.T) {
		n, err := ParseModule(`@a`, "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&InvalidAliasRelatedNode{
					NodeBase: NodeBase{
						NodeSpan{0, 2},
						&ParsingError{
							"unterminated AtHostLiteral | URLExpression | HostAliasDefinition",
							2,
							0,
							UnspecifiedCategory,
							nil,
						},
						nil,
					},
				},
			},
		}, n)
	})

	t.Run("integer literal", func(t *testing.T) {
		n := MustParseModule("12")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&IntLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
					Raw:      "12",
					Value:    12,
				},
			},
		}, n)
	})

	t.Run("float literal", func(t *testing.T) {
		n := MustParseModule("12.0")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&FloatLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Raw:      "12.0",
					Value:    12.0,
				},
			},
		}, n)
	})

	t.Run("quantity literal : integer", func(t *testing.T) {
		n := MustParseModule("1s")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&QuantityLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&QuantityLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&RateLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Quantity: &QuantityLiteral{
						NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
						Raw:      "1kB",
						Unit:     "kB",
						Value:    1.0,
					},
					Unit: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{4, 5},
							nil,
							nil,
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
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
					Raw:      `""`,
					Value:    ``,
				},
			},
		}, n)
	})

	t.Run("string literal : single space", func(t *testing.T) {
		n := MustParseModule(`" "`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
					Raw:      `" "`,
					Value:    ` `,
				},
			},
		}, n)
	})

	t.Run("string literal : single, non ASCII character", func(t *testing.T) {
		n := MustParseModule(`"é"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
					Raw:      `"é"`,
					Value:    `é`,
				},
			},
		}, n)
	})

	t.Run("string literal : one escaped backslashe", func(t *testing.T) {
		n := MustParseModule(`"\\"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Raw:      `"\\"`,
					Value:    `\`,
				},
			},
		}, n)
	})

	t.Run("string literal : two escaped backslashes", func(t *testing.T) {
		n := MustParseModule(`"\\\\"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
					Raw:      `"\\\\"`,
					Value:    `\\`,
				},
			},
		}, n)
	})

	t.Run("string literal : unterminated", func(t *testing.T) {
		n, err := ParseModule(`"ab`, "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&StringLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 3},
						&ParsingError{
							`unterminated string literal '"ab'`,
							3,
							0,
							KnownType,
							(*StringLiteral)(nil),
						},
						nil,
					},
					Raw:   `"ab`,
					Value: ``,
				},
			},
		}, n)
	})

	t.Run("rune literal : simple character", func(t *testing.T) {
		n := MustParseModule(`'a'`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&RuneLiteral{
					NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
					Value:    'a',
				},
			},
		}, n)
	})

	t.Run("rune literal : valid escaped character", func(t *testing.T) {
		n := MustParseModule(`'\n'`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&RuneLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Value:    '\n',
				},
			},
		}, n)
	})

	t.Run("rune literal : invalid escaped character", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule(`'\z'`)
		})
	})

	t.Run("rune literal : missing character", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule(`''`)
		})
	})

	t.Run("identifier literal : single letter", func(t *testing.T) {
		n := MustParseModule(`e`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 1}, nil, nil},

			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 1}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 1}, nil, nil},
						Name:     "e",
					},
					Must: true,
				},
			},
		}, n)
	})

	t.Run("identifier literal : letter followed by a digit", func(t *testing.T) {
		n := MustParseModule(`e2`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "e2",
					},
					Must: true,
				},
			},
		}, n)
	})

	t.Run("assignment var = <value>", func(t *testing.T) {
		n := MustParseModule("$a = $b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&Assignment{
					NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{5, 7}, nil, nil},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("assignment <index expr> = <value>", func(t *testing.T) {
		n := MustParseModule("$a[0] = $b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
			Statements: []Node{
				&Assignment{
					NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
					Left: &IndexExpression{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Indexed: &Variable{
							NodeBase: NodeBase{
								NodeSpan{0, 2},
								nil,
								nil,
							},
							Name: "a",
						},
						Index: &IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{3, 4},
								nil,
								nil,
							},
							Raw:   "0",
							Value: 0,
						},
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{8, 10}, nil, nil},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("assignment var = | <pipeline>", func(t *testing.T) {
		n := MustParseModule("$a = | a | b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 12}, nil, nil},
			Statements: []Node{
				&Assignment{
					NodeBase: NodeBase{NodeSpan{0, 12}, nil, nil},
					Left: &Variable{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "a",
					},
					Right: &PipelineExpression{
						NodeBase: NodeBase{NodeSpan{7, 12}, nil, nil},
						Stages: []*PipelineStage{
							{
								Kind: NormalStage,
								Expr: &Call{
									NodeBase: NodeBase{
										NodeSpan{7, 8},
										nil,
										nil,
									},
									Callee: &IdentifierLiteral{
										NodeBase: NodeBase{
											NodeSpan{7, 8},
											nil,
											nil,
										},
										Name: "a",
									},
									Must: true,
								},
							},
							{
								Kind: NormalStage,
								Expr: &Call{
									NodeBase: NodeBase{
										NodeSpan{11, 12},
										nil,
										nil,
									},
									Callee: &IdentifierLiteral{
										NodeBase: NodeBase{
											NodeSpan{11, 12},
											nil,
											nil,
										},
										Name: "b",
									},
									Must: true,
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("multi assignement statement : assign <ident> = <var>", func(t *testing.T) {
		n := MustParseModule("assign a = $b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 13}, nil, nil},
			Statements: []Node{
				&MultiAssignment{
					NodeBase: NodeBase{
						NodeSpan{0, 13},
						nil,
						[]Token{
							{ASSIGN_KEYWORD, NodeSpan{0, 6}},
						},
					},
					Variables: []Node{
						&IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{7, 8}, nil, nil},
							Name:     "a",
						},
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{11, 13}, nil, nil},
						Name:     "b",
					},
				},
			},
		}, n)
	})

	t.Run("multi assignement statement : assign var var = var", func(t *testing.T) {
		n := MustParseModule("assign a b = $c")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 15}, nil, nil},
			Statements: []Node{
				&MultiAssignment{
					NodeBase: NodeBase{
						NodeSpan{0, 15},
						nil,
						[]Token{
							{ASSIGN_KEYWORD, NodeSpan{0, 6}},
						},
					},
					Variables: []Node{
						&IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{7, 8}, nil, nil},
							Name:     "a",
						},
						&IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{9, 10}, nil, nil},
							Name:     "b",
						},
					},
					Right: &Variable{
						NodeBase: NodeBase{NodeSpan{13, 15}, nil, nil},
						Name:     "c",
					},
				},
			},
		}, n)
	})

	t.Run("call with paren : no args", func(t *testing.T) {
		n := MustParseModule("print()")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 12}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 12}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
							Name:     "a",
						},
						&Variable{
							NodeBase: NodeBase{NodeSpan{9, 11}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements: []Node{
				&Call{
					Must:     true,
					NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
			Statements: []Node{
				&Call{
					Must:     true,
					NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
							Name:     "a",
						},
						&Variable{
							NodeBase: NodeBase{NodeSpan{9, 11}, nil, nil},
							Name:     "b",
						},
					},
				},
			},
		}, n)
	})

	t.Run("call without paren: one arg with a delimiter", func(t *testing.T) {
		n := MustParseModule("print []")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements: []Node{
				&Call{
					Must:     true,
					NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&ListLiteral{
							NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
							Elements: nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("call without paren: followed by a single line comment", func(t *testing.T) {
		n := MustParseModule("print $a $b # comment")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 21}, nil, nil},
			Statements: []Node{
				&Call{
					Must:     true,
					NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&Variable{
							NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
							Name:     "a",
						},
						&Variable{
							NodeBase: NodeBase{NodeSpan{9, 11}, nil, nil},
							Name:     "b",
						},
					},
				},
			},
		}, n)
	})

	t.Run("pipeline statement: empty second stage", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("print $a |")
		})
	})

	t.Run("pipeline statement: second stage is not a call", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("print $a | 1")
		})
	})

	t.Run("pipeline statement: second stage is a call with no arguments", func(t *testing.T) {
		n := MustParseModule("print $a | do-something")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 23}, nil, nil},
			Statements: []Node{
				&PipelineStatement{
					NodeBase: NodeBase{NodeSpan{0, 23}, nil, nil},
					Stages: []*PipelineStage{
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
									Name:     "print",
								},
								Arguments: []Node{
									&Variable{
										NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
										Name:     "a",
									},
								},
							},
						},
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
									Name:     "do-something",
								},
								Arguments: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pipeline statement: second stage is a call with no arguments, followed by a ';'", func(t *testing.T) {
		n := MustParseModule("print $a | do-something;")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 24}, nil, nil},
			Statements: []Node{
				&PipelineStatement{
					NodeBase: NodeBase{NodeSpan{0, 23}, nil, nil},
					Stages: []*PipelineStage{
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
									Name:     "print",
								},
								Arguments: []Node{
									&Variable{
										NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
										Name:     "a",
									},
								},
							},
						},
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
									Name:     "do-something",
								},
								Arguments: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pipeline statement: second stage is a call with no arguments, followed by another statement on the following line", func(t *testing.T) {
		n := MustParseModule("print $a | do-something\n1")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 25}, nil, nil},
			Statements: []Node{
				&PipelineStatement{
					NodeBase: NodeBase{NodeSpan{0, 23}, nil, nil},
					Stages: []*PipelineStage{
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
									Name:     "print",
								},
								Arguments: []Node{
									&Variable{
										NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
										Name:     "a",
									},
								},
							},
						},
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
									Name:     "do-something",
								},
								Arguments: nil,
							},
						},
					},
				},
				&IntLiteral{
					NodeBase: NodeBase{
						NodeSpan{24, 25},
						nil,
						nil,
					},
					Raw:   "1",
					Value: 1,
				},
			},
		}, n)
	})

	t.Run("pipeline statement: first and second stages are calls with no arguments", func(t *testing.T) {
		n := MustParseModule("print | do-something")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 20}, nil, nil},
			Statements: []Node{
				&PipelineStatement{
					NodeBase: NodeBase{NodeSpan{0, 20}, nil, nil},
					Stages: []*PipelineStage{
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
									Name:     "print",
								},
							},
						},
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{8, 20}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{8, 20}, nil, nil},
									Name:     "do-something",
								},
								Arguments: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pipeline statement: second stage is a call with a single argument", func(t *testing.T) {
		n := MustParseModule("print $a | do-something $")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 25}, nil, nil},
			Statements: []Node{
				&PipelineStatement{
					NodeBase: NodeBase{NodeSpan{0, 25}, nil, nil},
					Stages: []*PipelineStage{
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
									Name:     "print",
								},
								Arguments: []Node{
									&Variable{
										NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
										Name:     "a",
									},
								},
							},
						},
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{11, 25}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
									Name:     "do-something",
								},
								Arguments: []Node{
									&Variable{
										NodeBase: NodeBase{NodeSpan{24, 25}, nil, nil},
										Name:     "",
									},
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pipeline statement: third stage is a call with no arguments", func(t *testing.T) {
		n := MustParseModule("print $a | do-something $ | do-something-else")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 45}, nil, nil},
			Statements: []Node{
				&PipelineStatement{
					NodeBase: NodeBase{NodeSpan{0, 45}, nil, nil},
					Stages: []*PipelineStage{
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
									Name:     "print",
								},
								Arguments: []Node{
									&Variable{
										NodeBase: NodeBase{NodeSpan{6, 8}, nil, nil},
										Name:     "a",
									},
								},
							},
						},
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{11, 25}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{11, 23}, nil, nil},
									Name:     "do-something",
								},
								Arguments: []Node{
									&Variable{
										NodeBase: NodeBase{NodeSpan{24, 25}, nil, nil},
										Name:     "",
									},
								},
							},
						},
						{
							Kind: NormalStage,
							Expr: &Call{
								Must:     true,
								NodeBase: NodeBase{NodeSpan{28, 45}, nil, nil},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{28, 45}, nil, nil},
									Name:     "do-something-else",
								},
								Arguments: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("identifier member expression", func(t *testing.T) {
		n := MustParseModule("http.get")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
					Callee: &IdentifierMemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
						Left: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
							Name:     "http",
						},
						PropertyNames: []*IdentifierLiteral{
							{
								NodeBase: NodeBase{NodeSpan{5, 8}, nil, nil},
								Name:     "get",
							},
						},
					},
					Must: true,
				},
			},
		}, n)
	})

	t.Run("identifier member expression with missing last property name", func(t *testing.T) {
		n, err := ParseModule("http.", "")

		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
					Callee: &IdentifierMemberExpression{
						NodeBase: NodeBase{
							NodeSpan{0, 5},
							&ParsingError{
								Message:        "unterminated identifier member expression",
								Index:          5,
								NodeStartIndex: 5,
								NodeCategory:   KnownType,
								NodeType:       (*IdentifierMemberExpression)(nil),
							},
							nil,
						},
						Left: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
							Name:     "http",
						},
						PropertyNames: nil,
					},
					Must: true,
				},
			},
		}, n)
	})

	t.Run("call <string> shorthand", func(t *testing.T) {
		n := MustParseModule(`mime"json"`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
					Must:     true,
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
						Name:     "mime",
					},
					Arguments: []Node{
						&StringLiteral{
							NodeBase: NodeBase{NodeSpan{4, 10}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
					Callee: &IdentifierMemberExpression{
						NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
						Left: &IdentifierLiteral{
							NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
							Name:     "http",
						},
						PropertyNames: []*IdentifierLiteral{
							{
								NodeBase: NodeBase{NodeSpan{5, 8}, nil, nil},
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
				nil,
				nil,
			},
			Statements: []Node{
				&Call{
					Must: true,
					NodeBase: NodeBase{
						NodeSpan{0, 7},
						nil,
						nil,
					},
					Callee: &IdentifierMemberExpression{
						NodeBase: NodeBase{
							NodeSpan{0, 3},
							nil,
							nil,
						},
						Left: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{0, 1},
								nil,
								nil,
							},
							Name: "a",
						},
						PropertyNames: []*IdentifierLiteral{
							{
								NodeBase: NodeBase{
									NodeSpan{2, 3},
									nil,
									nil,
								},
								Name: "b",
							},
						},
					},
					Arguments: []Node{
						&StringLiteral{
							NodeBase: NodeBase{
								NodeSpan{4, 7},
								nil,
								nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
						nil,
						nil,
					},
					Callee: &MemberExpression{
						NodeBase: NodeBase{
							NodeSpan{0, 4},
							nil,
							nil,
						},
						Left: &Variable{
							NodeBase: NodeBase{
								NodeSpan{0, 2},
								nil,
								nil,
							},
							Name: "a",
						},
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{3, 4},
								nil,
								nil,
							},
							Name: "b",
						},
					},
					Arguments: []Node{
						&StringLiteral{
							NodeBase: NodeBase{
								NodeSpan{5, 8},
								nil,
								nil,
							},
							Raw:   `"a"`,
							Value: "a",
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

	t.Run("call expression with no paren : single argument", func(t *testing.T) {
		n := MustParseModule("print$ 1")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
					Must:     true,
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{7, 8},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
						},
					},
				},
			},
		}, n)
	})

	t.Run("call expression with no paren : single argument that starts with a delimiter", func(t *testing.T) {
		n := MustParseModule("print$ (1)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
					Must:     true,
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{8, 9},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
						},
					},
				},
			},
		}, n)
	})

	t.Run("call expression with no paren : two arguments (literals)", func(t *testing.T) {
		n := MustParseModule("print$ 1 2")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
			Statements: []Node{
				&Call{
					NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
					Must:     true,
					Callee: &IdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
						Name:     "print",
					},
					Arguments: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{7, 8},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{9, 10},
								nil,
								nil,
							},
							Raw:   "2",
							Value: 2,
						},
					},
				},
			},
		}, n)
	})

	t.Run("empty single linge object literal 1", func(t *testing.T) {
		n := MustParseModule("{}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase:   NodeBase{NodeSpan{0, 2}, nil, nil},
					Properties: nil,
				},
			},
		}, n)
	})

	t.Run("empty single linge object literal 2", func(t *testing.T) {
		n := MustParseModule("{ }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 3}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase:   NodeBase{NodeSpan{0, 3}, nil, nil},
					Properties: nil,
				},
			},
		}, n)
	})

	t.Run("single line object literal { ident: integer} ", func(t *testing.T) {
		n := MustParseModule("{ a : 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}, nil, nil},
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

	t.Run("object literal : comments are only allowed between entries", func(t *testing.T) {
		MustParseModule("{ # comment \n}")
		MustParseModule("{ a : 1 # comment \n}")
		MustParseModule("{ # comment \n a : 1 }")

		assert.Panics(t, func() {
			MustParseModule("{ a : # comment \n 1 }")
		})

	})

	t.Run("single line object literal { : integer} ", func(t *testing.T) {
		n := MustParseModule("{ : 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 5}, nil, nil},
							Key:      nil,
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{4, 5}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 16}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 16}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}, nil, nil},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{9, 14}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{9, 10}, nil, nil},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{13, 14}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}, nil, nil},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{10, 15}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{10, 11}, nil, nil},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{14, 15}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 9}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{8, 9}, nil, nil},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{2, 9}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{5, 6}, nil, nil},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{8, 9}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 11}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{4, 9}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{4, 5}, nil, nil},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{8, 9}, nil, nil},
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
			NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{2, 7}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
								Name:     "a",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{6, 7}, nil, nil},
								Raw:      "1",
								Value:    1,
							},
						},
						{
							NodeBase: NodeBase{NodeSpan{10, 15}, nil, nil},
							Key: &IdentifierLiteral{
								NodeBase: NodeBase{NodeSpan{10, 11}, nil, nil},
								Name:     "b",
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{14, 15}, nil, nil},
								Raw:      "2",
								Value:    2,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line object literal : spread element ", func(t *testing.T) {
		n := MustParseModule("{ ... $e.{name} }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
			Statements: []Node{
				&ObjectLiteral{
					NodeBase:   NodeBase{NodeSpan{0, 17}, nil, nil},
					Properties: nil,
					SpreadElements: []*PropertySpreadElement{
						{
							NodeBase: NodeBase{
								NodeSpan{2, 15},
								nil,
								nil,
							},
							Extraction: &ExtractionExpression{
								NodeBase: NodeBase{
									NodeSpan{6, 15},
									nil,
									nil,
								},
								Object: &Variable{
									NodeBase: NodeBase{
										NodeSpan{6, 8},
										nil,
										nil,
									},
									Name: "e",
								},
								Keys: &KeyListExpression{
									NodeBase: NodeBase{
										NodeSpan{8, 15},
										nil,
										nil,
									},
									Keys: []*IdentifierLiteral{
										{
											NodeBase: NodeBase{
												NodeSpan{10, 14},
												nil,
												nil,
											},
											Name: "name",
										},
									},
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("empty single line list literal 1", func(t *testing.T) {
		n := MustParseModule("[]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 2},
				nil,
				nil,
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 2},
						nil,
						nil,
					},
					Elements: nil,
				},
			},
		}, n)
	})

	t.Run("empty single line list literal 2", func(t *testing.T) {
		n := MustParseModule("[ ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 3},
				nil,
				nil,
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 3},
						nil,
						nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
								nil,
								nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 7},
						nil,
						nil,
					}, Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{4, 5},
								nil,
								nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
						nil,
						nil,
					},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{6, 7},
								nil,
								nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
						nil,
						nil,
					},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
						},
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{6, 7},
								nil,
								nil,
							},
							Raw:   "2",
							Value: 2,
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line list literal [ integer <no space> <comma> ] ", func(t *testing.T) {
		n := MustParseModule("[ 1, ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 6},
				nil,
				nil,
			},
			Statements: []Node{
				&ListLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						nil,
					},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
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
				nil,
				nil,
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 11},
						nil,
						[]Token{
							{IF_KEYWORD, NodeSpan{0, 2}},
						},
					}, Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
							nil,
							nil,
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 11},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{8, 9}},
								{CLOSING_CURLY_BRACKET, NodeSpan{10, 11}},
							},
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
				nil,
				nil,
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 13},
						nil,
						[]Token{
							{IF_KEYWORD, NodeSpan{0, 2}},
						},
					}, Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
							nil,
							nil,
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 13},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{8, 9}},
								{CLOSING_CURLY_BRACKET, NodeSpan{12, 13}},
							},
						},
						Statements: []Node{
							&IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{10, 11},
									nil,
									nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 15},
						nil,
						[]Token{
							{IF_KEYWORD, NodeSpan{0, 2}},
						},
					},
					Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
							nil,
							nil,
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 15},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{8, 9}},
								{CLOSING_CURLY_BRACKET, NodeSpan{14, 15}},
							},
						},
						Statements: []Node{
							&Call{
								Must: true,
								NodeBase: NodeBase{
									NodeSpan{10, 13},
									nil,
									nil,
								},
								Callee: &IdentifierLiteral{
									NodeBase: NodeBase{
										NodeSpan{10, 11},
										nil,
										nil,
									},
									Name: "a",
								},
								Arguments: []Node{
									&IntLiteral{
										NodeBase: NodeBase{
											NodeSpan{12, 13},
											nil,
											nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 13},
						nil,
						[]Token{
							{IF_KEYWORD, NodeSpan{0, 2}},
						},
					},
					Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
							nil,
							nil,
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 13},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{8, 9}},
								{CLOSING_CURLY_BRACKET, NodeSpan{12, 13}},
							},
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
				nil,
				nil,
			},
			Statements: []Node{
				&IfStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 19},
						nil,
						[]Token{
							{IF_KEYWORD, NodeSpan{0, 2}},
							{ELSE_KEYWORD, NodeSpan{12, 16}},
						},
					}, Test: &BooleanLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 7},
							nil,
							nil,
						},
						Value: true,
					},
					Consequent: &Block{
						NodeBase: NodeBase{
							NodeSpan{8, 11},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{8, 9}},
								{CLOSING_CURLY_BRACKET, NodeSpan{10, 11}},
							},
						},
						Statements: nil,
					},
					Alternate: &Block{
						NodeBase: NodeBase{
							NodeSpan{17, 19},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{17, 18}},
								{CLOSING_CURLY_BRACKET, NodeSpan{18, 19}},
							},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("single line empty for <index>, <elem>  in statement", func(t *testing.T) {
		n := MustParseModule("for i, u in $users { }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 22},
				nil,
				nil,
			},
			Statements: []Node{
				&ForStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 22},
						nil,
						[]Token{
							{FOR_KEYWORD, NodeSpan{0, 3}},
							{COMMA, NodeSpan{5, 6}},
							{IN_KEYWORD, NodeSpan{9, 11}},
						},
					},
					KeyIndexIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{4, 5},
							nil,
							nil,
						},
						Name: "i",
					},
					ValueElemIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Name: "u",
					},
					IteratedValue: &Variable{
						NodeBase: NodeBase{
							NodeSpan{12, 18},
							nil,
							nil,
						},
						Name: "users",
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{19, 22},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{19, 20}},
								{CLOSING_CURLY_BRACKET, NodeSpan{21, 22}},
							},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("single line empty for <elem> in statement", func(t *testing.T) {
		n := MustParseModule("for u in $users { }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 19},
				nil,
				nil,
			},
			Statements: []Node{
				&ForStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 19},
						nil,
						[]Token{
							{FOR_KEYWORD, NodeSpan{0, 3}},
							{IN_KEYWORD, NodeSpan{6, 8}},
						},
					},
					KeyIndexIdent: nil,
					ValueElemIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{4, 5},
							nil,
							nil,
						},
						Name: "u",
					},
					IteratedValue: &Variable{
						NodeBase: NodeBase{
							NodeSpan{9, 15},
							nil,
							nil,
						},
						Name: "users",
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{16, 19},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{16, 17}},
								{CLOSING_CURLY_BRACKET, NodeSpan{18, 19}},
							},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("for .. in with break statement", func(t *testing.T) {
		n := MustParseModule("for i, u in $users { break }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 28},
				nil,
				nil,
			},
			Statements: []Node{
				&ForStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 28},
						nil,
						[]Token{
							{FOR_KEYWORD, NodeSpan{0, 3}},
							{COMMA, NodeSpan{5, 6}},
							{IN_KEYWORD, NodeSpan{9, 11}},
						},
					},
					KeyIndexIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{4, 5},
							nil,
							nil,
						},
						Name: "i",
					},
					ValueElemIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Name: "u",
					},
					IteratedValue: &Variable{
						NodeBase: NodeBase{
							NodeSpan{12, 18},
							nil,
							nil,
						},
						Name: "users",
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{19, 28},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{19, 20}},
								{CLOSING_CURLY_BRACKET, NodeSpan{27, 28}},
							},
						},
						Statements: []Node{
							&BreakStatement{
								NodeBase: NodeBase{
									NodeSpan{21, 26},
									nil,
									[]Token{{BREAK_KEYWORD, NodeSpan{21, 26}}},
								},
								Label: nil,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("for .. in with continue statement", func(t *testing.T) {
		n := MustParseModule("for i, u in $users { continue }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 31},
				nil,
				nil,
			},
			Statements: []Node{
				&ForStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 31},
						nil,
						[]Token{
							{FOR_KEYWORD, NodeSpan{0, 3}},
							{COMMA, NodeSpan{5, 6}},
							{IN_KEYWORD, NodeSpan{9, 11}},
						},
					},
					KeyIndexIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{4, 5},
							nil,
							nil,
						},
						Name: "i",
					},
					ValueElemIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Name: "u",
					},
					IteratedValue: &Variable{
						NodeBase: NodeBase{
							NodeSpan{12, 18},
							nil,
							nil,
						},
						Name: "users",
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{19, 31},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{19, 20}},
								{CLOSING_CURLY_BRACKET, NodeSpan{30, 31}},
							},
						},
						Statements: []Node{
							&ContinueStatement{
								NodeBase: NodeBase{
									NodeSpan{21, 29},
									nil,
									[]Token{{CONTINUE_KEYWORD, NodeSpan{21, 29}}},
								},
								Label: nil,
							},
						},
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
				nil,
				nil,
			},
			Statements: []Node{
				&ForStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 16},
						nil,
						[]Token{
							{FOR_KEYWORD, NodeSpan{0, 3}},
						},
					},
					KeyIndexIdent:  nil,
					ValueElemIdent: nil,
					IteratedValue: &BinaryExpression{
						NodeBase: NodeBase{
							NodeSpan{4, 12},
							nil,
							nil,
						},
						Operator: Range,
						Left: &IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{5, 6},
								nil,
								nil,
							},
							Raw:   "1",
							Value: 1,
						},
						Right: &IntLiteral{
							NodeBase: NodeBase{
								NodeSpan{10, 11},
								nil,
								nil,
							},
							Raw:   "2",
							Value: 2,
						},
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{13, 16},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{13, 14}},
								{CLOSING_CURLY_BRACKET, NodeSpan{15, 16}},
							},
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
				nil,
				nil,
			},
			Statements: []Node{
				&BinaryExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
						nil,
						nil,
					},
					Operator: Add,
					Left: &Variable{
						NodeBase: NodeBase{
							NodeSpan{1, 3},
							nil,
							nil,
						},
						Name: "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{
							NodeSpan{6, 8},
							nil,
							nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&BinaryExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 10},
						nil,
						nil,
					},
					Operator: Range,
					Left: &Variable{
						NodeBase: NodeBase{
							NodeSpan{1, 3},
							nil,
							nil,
						},
						Name: "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{
							NodeSpan{7, 9},
							nil,
							nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&BinaryExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 11},
						nil,
						nil,
					},
					Operator: ExclEndRange,
					Left: &Variable{
						NodeBase: NodeBase{
							NodeSpan{1, 3},
							nil,
							nil,
						},
						Name: "a",
					},
					Right: &Variable{
						NodeBase: NodeBase{
							NodeSpan{8, 10},
							nil,
							nil,
						},
						Name: "b",
					},
				},
			},
		}, n)
	})

	t.Run("binary expression : missing right operand", func(t *testing.T) {
		n, err := ParseModule("($a +)", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 6},
				nil,
				nil,
			},
			Statements: []Node{
				&BinaryExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						&ParsingError{
							"invalid binary expression: missing right operand",
							5,
							0,
							KnownType,
							(*BinaryExpression)(nil),
						},
						nil,
					},
					Operator: Add,
					Left: &Variable{
						NodeBase: NodeBase{
							NodeSpan{1, 3},
							nil,
							nil,
						},
						Name: "a",
					},
					Right: &MissingExpression{
						NodeBase: NodeBase{
							NodeSpan{4, 5},
							&ParsingError{
								"an expression was expected: ...($a +<<here>>)...",
								5,
								4,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
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
				nil,
				nil,
			},
			Statements: []Node{
				&UpperBoundRangeExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
						nil,
						nil,
					},
					UpperBound: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{2, 4},
							nil,
							nil,
						},
						Raw:   "10",
						Value: 10,
					},
				},
			},
		}, n)
	})

	t.Run("integer range literal", func(t *testing.T) {
		n := MustParseModule("1..2")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 4},
				nil,
				nil,
			},
			Statements: []Node{
				&IntegerRangeLiteral{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
						nil,
						nil,
					},
					LowerBound: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{0, 1},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
					UpperBound: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 4},
							nil,
							nil,
						},
						Raw:   "2",
						Value: 2,
					},
				},
			},
		}, n)
	})

	t.Run("rune range expression", func(t *testing.T) {
		n := MustParseModule("'a'..'z'")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 8},
				nil,
				nil,
			},
			Statements: []Node{
				&RuneRangeExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 8},
						nil,
						nil,
					},
					Lower: &RuneLiteral{
						NodeBase: NodeBase{
							NodeSpan{0, 3},
							nil,
							nil,
						},
						Value: 'a',
					},
					Upper: &RuneLiteral{
						NodeBase: NodeBase{
							NodeSpan{5, 8},
							nil,
							nil,
						},
						Value: 'z',
					},
				},
			},
		}, n)
	})
	t.Run("invalid rune range expression : <rune> '.'", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("'a'.")
		})
	})

	t.Run("invalid rune range expression : <rune> '.' '.' ", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("'a'..")
		})
	})

	t.Run("function expression : no parameters, no requirements, empty body ", func(t *testing.T) {
		n := MustParseModule("fn(){}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 6},
				nil,
				nil,
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						[]Token{{FN_KEYWORD, NodeSpan{0, 2}}},
					},
					Parameters: nil,
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{4, 6},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{4, 5}},
								{CLOSING_CURLY_BRACKET, NodeSpan{5, 6}},
							},
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
				nil,
				nil,
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 18},
						nil,
						[]Token{{FN_KEYWORD, NodeSpan{0, 2}}},
					},
					Parameters: nil,
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{16, 18},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{16, 17}},
								{CLOSING_CURLY_BRACKET, NodeSpan{17, 18}},
							},
						},
						Statements: nil,
					},
					Requirements: &Requirements{
						ValuelessTokens: []Token{
							{REQUIRE_KEYWORD, NodeSpan{5, 12}},
						},
						Object: &ObjectLiteral{
							NodeBase: NodeBase{
								NodeSpan{13, 15},
								nil,
								nil,
							},
							Properties: nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("function expression : single parameter, empty body ", func(t *testing.T) {
		n := MustParseModule("fn(x){}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 7},
				nil,
				nil,
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 7},
						nil,
						[]Token{{FN_KEYWORD, NodeSpan{0, 2}}},
					},
					Parameters: []*FunctionParameter{
						{
							NodeBase: NodeBase{
								NodeSpan{3, 4},
								nil,
								nil,
							},
							Var: &IdentifierLiteral{
								NodeBase: NodeBase{
									NodeSpan{3, 4},
									nil,
									nil,
								},
								Name: "x",
							},
						},
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{5, 7},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{5, 6}},
								{CLOSING_CURLY_BRACKET, NodeSpan{6, 7}},
							},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("function expression : two parameters, empty body ", func(t *testing.T) {
		n := MustParseModule("fn(x,n){}")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 9},
				nil,
				nil,
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
						nil,
						[]Token{
							{FN_KEYWORD, NodeSpan{0, 2}},
						},
					},
					Parameters: []*FunctionParameter{
						{
							NodeBase: NodeBase{
								NodeSpan{3, 4},
								nil,
								nil,
							},
							Var: &IdentifierLiteral{
								NodeBase: NodeBase{
									NodeSpan{3, 4},
									nil,
									nil,
								},
								Name: "x",
							},
						},
						{
							NodeBase: NodeBase{
								NodeSpan{5, 6},
								nil,
								nil,
							},
							Var: &IdentifierLiteral{
								NodeBase: NodeBase{
									NodeSpan{5, 6},
									nil,
									nil,
								},
								Name: "n",
							},
						},
					},
					Body: &Block{
						NodeBase: NodeBase{
							NodeSpan{7, 9},
							nil,
							[]Token{
								{OPENING_CURLY_BRACKET, NodeSpan{7, 8}},
								{CLOSING_CURLY_BRACKET, NodeSpan{8, 9}},
							},
						},
						Statements: nil,
					},
				},
			},
		}, n)
	})

	t.Run("function expression : parameter list not followed by a block ", func(t *testing.T) {
		n, err := ParseModule("fn()1", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 5},
				nil,
				nil,
			},
			Statements: []Node{
				&FunctionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
						&ParsingError{
							"function : parameter list should be followed by a block, not 1",
							4,
							0,
							UnspecifiedCategory,
							nil,
						},
						[]Token{{FN_KEYWORD, NodeSpan{0, 2}}},
					},
					Parameters: nil,
					Body:       nil,
				},
				&IntLiteral{
					NodeBase: NodeBase{
						NodeSpan{4, 5},
						nil,
						nil,
					},
					Raw:   "1",
					Value: 1,
				},
			},
		}, n)
	})

	t.Run("lazy expression : '@' '(' integer ')' ", func(t *testing.T) {
		n := MustParseModule("@(1)")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 4},
				nil,
				nil,
			},
			Statements: []Node{
				&LazyExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
						nil,
						nil,
					},
					Expression: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{2, 3},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
				},
			},
		}, n)
	})

	t.Run("lazy expression followed by another expression", func(t *testing.T) {
		n := MustParseModule("@(1) 2")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 6},
				nil,
				nil,
			},
			Statements: []Node{
				&LazyExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
						nil,
						nil,
					},
					Expression: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{2, 3},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
				},
				&IntLiteral{
					NodeBase: NodeBase{
						NodeSpan{5, 6},
						nil,
						nil,
					},
					Raw:   "2",
					Value: 2,
				},
			},
		}, n)
	})

	t.Run("switch statement : empty", func(t *testing.T) {
		n := MustParseModule("switch 1 { }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 12},
				nil,
				nil,
			},
			Statements: []Node{
				&SwitchStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 12},
						nil,
						[]Token{
							{SWITCH_KEYWORD, NodeSpan{0, 6}},
						},
					},
					Discriminant: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
					Cases: nil,
				},
			},
		}, n)
	})

	t.Run("switch statement : single case", func(t *testing.T) {
		n := MustParseModule("switch 1 { 1 { } }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 18},
				nil,
				nil,
			},
			Statements: []Node{
				&SwitchStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 18},
						nil,
						[]Token{
							{SWITCH_KEYWORD, NodeSpan{0, 6}},
						},
					},
					Discriminant: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
					Cases: []*Case{
						{
							NodeBase: NodeBase{
								NodeSpan{11, 16},
								nil,
								nil,
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{11, 12},
									nil,
									nil,
								},
								Raw:   "1",
								Value: 1,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{13, 16},
									nil,
									[]Token{
										{OPENING_CURLY_BRACKET, NodeSpan{13, 14}},
										{CLOSING_CURLY_BRACKET, NodeSpan{15, 16}},
									},
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
				nil,
				nil,
			},
			Statements: []Node{
				&SwitchStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 24},
						nil,
						[]Token{
							{SWITCH_KEYWORD, NodeSpan{0, 6}},
						},
					},
					Discriminant: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
					Cases: []*Case{
						{
							NodeBase: NodeBase{
								NodeSpan{11, 16},
								nil,
								nil,
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{11, 12},
									nil,
									nil,
								},
								Raw:   "1",
								Value: 1,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{13, 16},
									nil,
									[]Token{
										{OPENING_CURLY_BRACKET, NodeSpan{13, 14}},
										{CLOSING_CURLY_BRACKET, NodeSpan{15, 16}},
									},
								},
								Statements: nil,
							},
						},
						{
							NodeBase: NodeBase{
								NodeSpan{17, 22},
								nil,
								nil,
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{17, 18},
									nil,
									nil,
								},
								Raw:   "2",
								Value: 2,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{19, 22},
									nil,
									[]Token{
										{OPENING_CURLY_BRACKET, NodeSpan{19, 20}},
										{CLOSING_CURLY_BRACKET, NodeSpan{21, 22}},
									},
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
				nil,
				nil,
			},
			Statements: []Node{
				&SwitchStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 21},
						nil,
						[]Token{
							{SWITCH_KEYWORD, NodeSpan{0, 6}},
						},
					},
					Discriminant: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
					Cases: []*Case{
						{
							NodeBase: NodeBase{
								NodeSpan{11, 19},
								nil,
								nil,
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{11, 12},
									nil,
									nil,
								},
								Raw:   "1",
								Value: 1,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{16, 19},
									nil,
									[]Token{
										{OPENING_CURLY_BRACKET, NodeSpan{16, 17}},
										{CLOSING_CURLY_BRACKET, NodeSpan{18, 19}},
									},
								},
								Statements: nil,
							},
						},
						{
							NodeBase: NodeBase{
								NodeSpan{14, 19},
								nil,
								nil,
							},
							Value: &IntLiteral{
								NodeBase: NodeBase{
									NodeSpan{14, 15},
									nil,
									nil,
								},
								Raw:   "2",
								Value: 2,
							},
							Block: &Block{
								NodeBase: NodeBase{
									NodeSpan{16, 19},
									nil,
									[]Token{
										{OPENING_CURLY_BRACKET, NodeSpan{16, 17}},
										{CLOSING_CURLY_BRACKET, NodeSpan{18, 19}},
									},
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
				nil,
				nil,
			},
			Statements: nil,
		}, n)
	})

	t.Run("not empty single line comment", func(t *testing.T) {
		n := MustParseModule("# some text")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 11},
				nil,
				nil,
			},
			Statements: nil,
		}, n)
	})

	t.Run("import statement", func(t *testing.T) {
		n := MustParseModule(`import a https://example.com/a.gos "sS1pD9weZBuJdFmowNwbpi7BJ8TNftyUImj/0WQi72jY=" {} allow {}`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 94},
				nil,
				nil,
			},
			Statements: []Node{
				&ImportStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 94},
						nil,
						[]Token{
							{IMPORT_KEYWORD, NodeSpan{0, 6}},
							{ALLOW_KEYWORD, NodeSpan{86, 91}},
						},
					},
					Identifier: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Name: "a",
					},
					ValidationString: &StringLiteral{
						NodeBase: NodeBase{
							NodeSpan{35, 82},
							nil,
							nil,
						},
						Raw:   `"sS1pD9weZBuJdFmowNwbpi7BJ8TNftyUImj/0WQi72jY="`,
						Value: "sS1pD9weZBuJdFmowNwbpi7BJ8TNftyUImj/0WQi72jY=",
					},
					URL: &URLLiteral{
						NodeBase: NodeBase{
							NodeSpan{9, 34},
							nil,
							nil,
						},
						Value: "https://example.com/a.gos",
					},
					ArgumentObject: &ObjectLiteral{
						NodeBase: NodeBase{
							NodeSpan{83, 85},
							nil,
							nil,
						},
						Properties: nil,
					},
					GrantedPermissions: &ObjectLiteral{
						NodeBase: NodeBase{
							NodeSpan{92, 94},
							nil,
							nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 10},
						nil,
						[]Token{{SPAWN_KEYWORD, NodeSpan{0, 2}}},
					},
					Globals: &NilLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 6},
							nil,
							nil,
						},
					},
					ExprOrVar: &Call{
						NodeBase: NodeBase{
							NodeSpan{7, 10},
							nil,
							nil,
						},
						Callee: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{7, 8},
								nil,
								nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 21},
						nil,
						[]Token{{SPAWN_KEYWORD, NodeSpan{0, 2}}},
					},
					Globals: &NilLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 6},
							nil,
							nil,
						},
					},
					ExprOrVar: &EmbeddedModule{
						NodeBase: NodeBase{
							NodeSpan{7, 21},
							nil,
							nil,
						},
						Requirements: &Requirements{
							[]Token{
								{REQUIRE_KEYWORD, NodeSpan{9, 16}},
							},
							&ObjectLiteral{
								NodeBase: NodeBase{
									NodeSpan{17, 19},
									nil,
									nil,
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
				nil,
				nil,
			},
			Statements: []Node{
				&SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 16},
						nil,
						[]Token{{SPAWN_KEYWORD, NodeSpan{0, 2}}},
					},
					GroupIdent: &IdentifierLiteral{
						NodeBase: NodeBase{
							NodeSpan{3, 8},
							nil,
							nil,
						},
						Name: "group",
					},
					Globals: &NilLiteral{
						NodeBase: NodeBase{
							NodeSpan{9, 12},
							nil,
							nil,
						},
					},
					ExprOrVar: &Call{
						NodeBase: NodeBase{
							NodeSpan{13, 16},
							nil,
							nil,
						},
						Callee: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{13, 14},
								nil,
								nil,
							},
							Name: "f",
						},
					},
				},
			},
		}, n)
	})

	//also used for checking block parsing
	t.Run("permission dropping statement : empty object literal", func(t *testing.T) {
		n := MustParseModule("drop-perms {}")

		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 13},
				nil,
				nil,
			},
			Statements: []Node{
				&PermissionDroppingStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 13},
						nil,
						[]Token{{DROP_PERMS_KEYWORD, NodeSpan{0, 10}}},
					},
					Object: &ObjectLiteral{
						NodeBase: NodeBase{
							NodeSpan{11, 13},
							nil,
							nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("permission dropping statement : value is not an object literal", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("drop-perms 1")
		})
	})

	t.Run("permission dropping statement : value is not an object literal", func(t *testing.T) {
		assert.Panics(t, func() {
			MustParseModule("drop-perms")
		})
	})

	t.Run("return statement : value", func(t *testing.T) {
		n := MustParseModule("return 1")

		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 8},
				nil,
				nil,
			},
			Statements: []Node{
				&ReturnStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 8},
						nil,
						[]Token{{RETURN_KEYWORD, NodeSpan{0, 6}}},
					},
					Expr: &IntLiteral{
						NodeBase: NodeBase{
							NodeSpan{7, 8},
							nil,
							nil,
						},
						Raw:   "1",
						Value: 1,
					},
				},
			},
		}, n)
	})

	t.Run("return statement : no value", func(t *testing.T) {
		n := MustParseModule("return")

		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 6},
				nil,
				nil,
			},
			Statements: []Node{
				&ReturnStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						[]Token{{RETURN_KEYWORD, NodeSpan{0, 6}}},
					},
				},
			},
		}, n)
	})

	t.Run("return statement : no value, followed by newline", func(t *testing.T) {
		n := MustParseModule("return\n")

		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 7},
				nil,
				nil,
			},
			Statements: []Node{
				&ReturnStatement{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						[]Token{{RETURN_KEYWORD, NodeSpan{0, 6}}},
					},
				},
			},
		}, n)
	})

	t.Run("boolean conversion expression", func(t *testing.T) {
		n := MustParseModule("$err?")

		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{
				NodeSpan{0, 5},
				nil,
				nil,
			},
			Statements: []Node{
				&BooleanConversionExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Expr: &Variable{
						NodeBase: NodeBase{
							NodeSpan{0, 4},
							nil,
							nil,
						},
						Name: "err",
					},
				},
			},
		}, n)
	})

	t.Run("pattern identifier literal", func(t *testing.T) {
		n := MustParseModule("%int")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&PatternIdentifierLiteral{
					NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
					Name:     "int",
				},
			},
		}, n)
	})

	t.Run("single line object pattern literal { : integer} ", func(t *testing.T) {
		n := MustParseModule("%{ : 1 }")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements: []Node{
				&ObjectPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
					Properties: []ObjectProperty{
						{
							NodeBase: NodeBase{NodeSpan{3, 6}, nil, nil},
							Key:      nil,
							Value: &IntLiteral{
								NodeBase: NodeBase{NodeSpan{5, 6}, nil, nil},
								Raw:      "1",
								Value:    1,
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("single line object pattern literal [ integer ] ", func(t *testing.T) {
		n := MustParseModule("%[ 1 ]")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&ListPatternLiteral{
					NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
					Elements: []Node{
						&IntLiteral{
							NodeBase: NodeBase{NodeSpan{3, 4}, nil, nil},
							Raw:      "1",
							Value:    1,
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a pattern identifier literal ", func(t *testing.T) {
		n := MustParseModule("%i = %int;")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 10}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 10},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "i",
					},
					Right: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{5, 9}, nil, nil},
						Name:     "int",
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is an object pattern literal ", func(t *testing.T) {
		n := MustParseModule("%i = %{ : 1 };")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 14}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 14},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "i",
					},
					Right: &ObjectPatternLiteral{
						NodeBase: NodeBase{NodeSpan{5, 13}, nil, nil},
						Properties: []ObjectProperty{
							{
								NodeBase: NodeBase{NodeSpan{8, 11}, nil, nil},
								Key:      nil,
								Value: &IntLiteral{
									NodeBase: NodeBase{NodeSpan{10, 11}, nil, nil},
									Raw:      "1",
									Value:    1,
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a single element pattern of kind string : element is a string literal", func(t *testing.T) {
		n := MustParseModule("%l = string \"a\";")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 16}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 16},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "l",
					},
					Right: &PatternPiece{
						NodeBase: NodeBase{NodeSpan{5, 15}, nil, nil},
						Kind:     StringPattern,
						Elements: []*PatternPieceElement{
							{
								NodeBase: NodeBase{
									NodeSpan{12, 15},
									nil,
									nil,
								},
								Expr: &StringLiteral{
									NodeBase: NodeBase{NodeSpan{12, 15}, nil, nil},
									Raw:      "\"a\"",
									Value:    "a",
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a single element pattern of kind string : element is a rune literal", func(t *testing.T) {
		n := MustParseModule("%l = string 'a';")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 16}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 16},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "l",
					},
					Right: &PatternPiece{
						NodeBase: NodeBase{NodeSpan{5, 15}, nil, nil},
						Kind:     StringPattern,
						Elements: []*PatternPieceElement{
							{
								NodeBase: NodeBase{
									NodeSpan{12, 15},
									nil,
									nil,
								},
								Expr: &RuneLiteral{
									NodeBase: NodeBase{NodeSpan{12, 15}, nil, nil},
									Value:    'a',
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a single element pattern of kind string : element is a parenthesised string literal", func(t *testing.T) {
		n := MustParseModule("%l = string (\"a\");")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 18}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 18},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "l",
					},
					Right: &PatternPiece{
						NodeBase: NodeBase{NodeSpan{5, 17}, nil, nil},
						Kind:     StringPattern,
						Elements: []*PatternPieceElement{
							{
								NodeBase: NodeBase{
									NodeSpan{12, 17},
									nil,
									nil,
								},
								Expr: &StringLiteral{
									NodeBase: NodeBase{NodeSpan{13, 16}, nil, nil},
									Raw:      "\"a\"",
									Value:    "a",
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a single element pattern of kind string : element is a parenthesised string literal with '*' as ocurrence", func(t *testing.T) {
		n := MustParseModule("%l = string (\"a\")*;")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 19}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 19},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "l",
					},
					Right: &PatternPiece{
						NodeBase: NodeBase{NodeSpan{5, 18}, nil, nil},
						Kind:     StringPattern,
						Elements: []*PatternPieceElement{
							{
								Ocurrence: ZeroOrMoreOcurrence,
								NodeBase: NodeBase{
									NodeSpan{12, 18},
									nil,
									nil,
								},
								Expr: &StringLiteral{
									NodeBase: NodeBase{NodeSpan{13, 16}, nil, nil},
									Raw:      "\"a\"",
									Value:    "a",
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a single element pattern of kind string : element is a parenthesised string literal with '=2' as ocurrence", func(t *testing.T) {
		n := MustParseModule("%l = string (\"a\")=2;")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 20}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 20},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "l",
					},
					Right: &PatternPiece{
						NodeBase: NodeBase{NodeSpan{5, 19}, nil, nil},
						Kind:     StringPattern,
						Elements: []*PatternPieceElement{
							{
								Ocurrence:           ExactOcurrence,
								ExactOcurrenceCount: 2,
								NodeBase: NodeBase{
									NodeSpan{12, 19},
									nil,
									nil,
								},
								Expr: &StringLiteral{
									NodeBase: NodeBase{NodeSpan{13, 16}, nil, nil},
									Raw:      "\"a\"",
									Value:    "a",
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a single element pattern of kind string : element is a pattern identifier literal with '=2' as ocurrence", func(t *testing.T) {
		n := MustParseModule("%l = string %s=2;")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 17},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "l",
					},
					Right: &PatternPiece{
						NodeBase: NodeBase{NodeSpan{5, 16}, nil, nil},
						Kind:     StringPattern,
						Elements: []*PatternPieceElement{
							{
								Ocurrence:           ExactOcurrence,
								ExactOcurrenceCount: 2,
								NodeBase: NodeBase{
									NodeSpan{12, 16},
									nil,
									nil,
								},
								Expr: &PatternIdentifierLiteral{
									NodeBase: NodeBase{NodeSpan{12, 14}, nil, nil},
									Name:     "s",
								},
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : RHS is a two-case union with one element each", func(t *testing.T) {
		n := MustParseModule(`%i = | "a" | "b";`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 17}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 17},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "i",
					},
					Right: &PatternUnion{
						NodeBase: NodeBase{
							NodeSpan{5, 16},
							nil,
							nil,
						},
						Cases: []Node{
							&StringLiteral{
								NodeBase: NodeBase{NodeSpan{7, 10}, nil, nil},
								Raw:      `"a"`,
								Value:    "a",
							},
							&StringLiteral{
								NodeBase: NodeBase{NodeSpan{13, 16}, nil, nil},
								Raw:      `"b"`,
								Value:    "b",
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : missing RHS (the semicolon is present)", func(t *testing.T) {
		n, err := ParseModule("%i =;", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "i",
					},
					Right: &InvalidComplexPatternElement{
						NodeBase: NodeBase{
							NodeSpan{4, 4},
							&ParsingError{
								"a pattern was expected: ...%i =<<here>>;...",
								4,
								4,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : missing RHS (no semicolon)", func(t *testing.T) {
		n, err := ParseModule("%i =", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "i",
					},
					Right: &InvalidComplexPatternElement{
						NodeBase: NodeBase{
							NodeSpan{4, 4},
							&ParsingError{
								"a pattern was expected: ...%i =<<here>>",
								4,
								4,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("pattern definition : missing RHS (no semicolon)", func(t *testing.T) {
		n, err := ParseModule("%i =", "")
		assert.Error(t, err)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 4}, nil, nil},
			Statements: []Node{
				&PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{0, 4},
						nil,
						nil,
					},
					Left: &PatternIdentifierLiteral{
						NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
						Name:     "i",
					},
					Right: &InvalidComplexPatternElement{
						NodeBase: NodeBase{
							NodeSpan{4, 4},
							&ParsingError{
								"a pattern was expected: ...%i =<<here>>",
								4,
								4,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : single element : type selector", func(t *testing.T) {
		n := MustParseModule("s!div")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Elements: []Node{
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 5},
								nil,
								nil,
							},
							Name: "div",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : selector followed by newline", func(t *testing.T) {

		n := MustParseModule("s!div\n")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Elements: []Node{
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 5},
								nil,
								nil,
							},
							Name: "div",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : selector followed by exclamation mark", func(t *testing.T) {

		n := MustParseModule("s!div!")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						nil,
					},
					Elements: []Node{
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 5},
								nil,
								nil,
							},
							Name: "div",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : selector followed by exclamation mark and an expression", func(t *testing.T) {

		n := MustParseModule("s!div! 1")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 8}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						nil,
					},
					Elements: []Node{
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 5},
								nil,
								nil,
							},
							Name: "div",
						},
					},
				},
				&IntLiteral{
					NodeBase: NodeBase{
						NodeSpan{7, 8},
						nil,
						nil,
					},
					Raw:   "1",
					Value: 1,
				},
			},
		}, n)
	})

	t.Run("css selector : single element : class selector", func(t *testing.T) {
		n := MustParseModule("s!.ab")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Elements: []Node{
						&CssClassSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 5},
								nil,
								nil,
							},
							Name: "ab",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : single element : pseudo class selector", func(t *testing.T) {
		n := MustParseModule("s!:ab")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Elements: []Node{
						&CssPseudoClassSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 5},
								nil,
								nil,
							},
							Name: "ab",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : single element : pseudo element selector", func(t *testing.T) {
		n := MustParseModule("s!::ab")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						nil,
					},
					Elements: []Node{
						&CssPseudoElementSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 6},
								nil,
								nil,
							},
							Name: "ab",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : single element : pseudo element selector", func(t *testing.T) {
		n := MustParseModule("s!::ab")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 6}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 6},
						nil,
						nil,
					},
					Elements: []Node{
						&CssPseudoElementSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 6},
								nil,
								nil,
							},
							Name: "ab",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : single element : attribute selector", func(t *testing.T) {
		n := MustParseModule(`s![a="1"]`)
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 9}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 9},
						nil,
						nil,
					},
					Elements: []Node{
						&CssAttributeSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 9},
								nil,
								nil,
							},
							AttributeName: &IdentifierLiteral{
								NodeBase: NodeBase{
									NodeSpan{3, 4},
									nil,
									nil,
								},
								Name: "a",
							},
							Matcher: "=",
							Value: &StringLiteral{
								NodeBase: NodeBase{
									NodeSpan{5, 8},
									nil,
									nil,
								},
								Raw:   `"1"`,
								Value: "1",
							},
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : direct child", func(t *testing.T) {
		n := MustParseModule("s!a > b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 7}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 7},
						nil,
						nil,
					},
					Elements: []Node{
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
								nil,
								nil,
							},
							Name: "a",
						},
						&CssCombinator{
							NodeBase: NodeBase{
								NodeSpan{4, 5},
								nil,
								nil,
							},
							Name: ">",
						},
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{6, 7},
								nil,
								nil,
							},
							Name: "b",
						},
					},
				},
			},
		}, n)
	})

	t.Run("css selector : descendant", func(t *testing.T) {
		n := MustParseModule("s!a b")
		assert.EqualValues(t, &Module{
			NodeBase: NodeBase{NodeSpan{0, 5}, nil, nil},
			Statements: []Node{
				&CssSelectorExpression{
					NodeBase: NodeBase{
						NodeSpan{0, 5},
						nil,
						nil,
					},
					Elements: []Node{
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{2, 3},
								nil,
								nil,
							},
							Name: "a",
						},
						&CssCombinator{
							NodeBase: NodeBase{
								NodeSpan{3, 4},
								nil,
								nil,
							},
							Name: " ",
						},
						&CssTypeSelector{
							NodeBase: NodeBase{
								NodeSpan{4, 5},
								nil,
								nil,
							},
							Name: "b",
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

func (user User) GetNameNoCtx() string {
	return user.Name
}

func ctxlessFunc() int {
	return 3
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

	t.Run("object literal with duplicate key prop : two explicit keys : one in spread element", func(t *testing.T) {
		n := MustParseModule(`{"a": 1, ... $e.{a}}`)
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

	t.Run("assignment with a function's name", func(t *testing.T) {
		n := MustParseModule(`
			fn f(){}

			$$f = 0
		`)
		assert.Error(t, Check(n))
	})

	t.Run("function declaration with the same name as a global variable assignment", func(t *testing.T) {
		n := MustParseModule(`
			$$f = 0

			fn f(){}
		`)
		assert.Error(t, Check(n))
	})

	t.Run("assignement of a constant", func(t *testing.T) {
		n := MustParseModule(`
			const (
				a = 1
			)

			$$a = 0
		`)
		assert.Error(t, Check(n))
	})

	t.Run("break statement : direct child of a for statement", func(t *testing.T) {
		n := MustParseModule(`
			for i, e in [] {
				break
			}
		`)
		assert.NoError(t, Check(n))
	})

	t.Run("break statement : in an if statement in a for statement", func(t *testing.T) {
		n := MustParseModule(`
			for i, e in [] {
				if true {
					break
				}
			}
		`)
		assert.NoError(t, Check(n))
	})

	t.Run("break statement : direct child of a module", func(t *testing.T) {
		n := MustParseModule(`
			break
		`)
		assert.Error(t, Check(n))
	})

	t.Run("break statement : direct child of an embedded module", func(t *testing.T) {
		n := MustParseModule(`
			sr nil {
				break
			}
		`)
		assert.Error(t, Check(n))
	})

	t.Run("local variable in a module : undefined", func(t *testing.T) {
		n := MustParseModule(`
			$a
		`)
		assert.Error(t, Check(n))
	})

	t.Run("local variable in a module : defined", func(t *testing.T) {
		n := MustParseModule(`
			a = 1
			$a
		`)
		assert.NoError(t, Check(n))
	})

	t.Run("local variable in an embedded module : undefined", func(t *testing.T) {
		n := MustParseModule(`
			a = 1
			sr nil {
				$a
			}
		`)
		assert.Error(t, Check(n))
	})

	t.Run("local variable in a function : undefined", func(t *testing.T) {
		n := MustParseModule(`
			a = 1
			fn f(){
				$a
			}
		`)
		assert.Error(t, Check(n))
	})

	t.Run("local variable in a function : defined", func(t *testing.T) {
		n := MustParseModule(`
			fn f(){
				a = 1
				$a
			}
		`)
		assert.NoError(t, Check(n))
	})

	t.Run("local variable in a lazy expression", func(t *testing.T) {
		n := MustParseModule(`
			@($a)
		`)
		assert.NoError(t, Check(n))
	})

	t.Run("argument variable in a function", func(t *testing.T) {
		n := MustParseModule(`
			fn f(a){
				$a
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
				URL = https://example.com/
			)
			require { 
				read: $$URL
			}
		`, []Permission{
			HttpPermission{ReadPerm, URL("https://example.com/")},
		}, []Limitation{}},
		{"call_contextless_func_and_method", `
			require { 
				use: {
					contextless: {
						: f,
						User: {
							Name: {}
						}
					}
				}
			}
		`, []Permission{
			ContextlessCallPermission{ReceiverTypeName: "", FuncMethodName: "f"},
			ContextlessCallPermission{ReceiverTypeName: "User", FuncMethodName: "Name"},
		}, []Limitation{}},
		{"limitations", `
			require { 
				limits: {
					"http/upload": 100kB/s
					"fs/new-file": 100x/s
				}
			}
		`, []Permission{}, []Limitation{
			{Name: "http/upload", ByteRate: ByteRate(100_000)},
			{Name: "fs/new-file", SimpleRate: SimpleRate(100)},
		}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mod := MustParseModule(testCase.inputModule)
			perms, limitations := mod.Requirements.Object.PermissionsLimitations(mod.GlobalConstantDeclarations, nil, nil, nil)
			assert.EqualValues(t, testCase.expectedPermissions, perms)
			assert.EqualValues(t, testCase.expectedLimitations, limitations)
		})
	}

}

func NewDefaultTestContext() *Context {
	return NewContext([]Permission{
		GlobalVarPermission{ReadPerm, "*"},
		GlobalVarPermission{UpdatePerm, "*"},
		GlobalVarPermission{CreatePerm, "*"},
		GlobalVarPermission{UsePerm, "*"},

		HttpPermission{ReadPerm, HTTPHostPattern("https://*")},
		RoutinePermission{CreatePerm},
	}, nil, nil)
}

func TestEval(t *testing.T) {

	t.Run("integer literal", func(t *testing.T) {
		n := MustParseModule("1")
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("string literal", func(t *testing.T) {
		n := MustParseModule(`"a"`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.EqualValues(t, "a", res)
	})

	t.Run("boolean literal", func(t *testing.T) {
		n := MustParseModule(`true`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.EqualValues(t, true, res)
	})

	t.Run("nil literal", func(t *testing.T) {
		n := MustParseModule(`nil`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.EqualValues(t, nil, res)
	})

	t.Run("absolute path literal", func(t *testing.T) {
		n := MustParseModule(`/`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.Equal(t, Path("/"), res)
	})

	t.Run("relative path literal", func(t *testing.T) {
		n := MustParseModule(`./`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.Equal(t, Path("./"), res)
	})

	t.Run("absolute path pattern literal", func(t *testing.T) {
		n := MustParseModule(`/*`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.Equal(t, PathPattern("/*"), res)
	})

	t.Run("relative path pattern literal", func(t *testing.T) {
		n := MustParseModule(`./*`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.Equal(t, PathPattern("./*"), res)
	})

	t.Run("named-segment path pattern literal", func(t *testing.T) {
		n := MustParseModule(`%/home/$username$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.IsType(t, NamedSegmentPathPattern{}, res)
	})

	t.Run("absolute path expression : interpolation value is a string", func(t *testing.T) {
		n := MustParseModule(`/home/$username$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"username": "foo",
		}))
		assert.NoError(t, err)
		assert.Equal(t, Path("/home/foo"), res)
	})

	t.Run("absolute path expression : interpolation value is a string containing '/'", func(t *testing.T) {
		n := MustParseModule(`/home/$username$`)
		_, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"username": "fo/o",
		}))
		assert.Error(t, err)
	})

	t.Run("absolute path expression : interpolation value is a relative path", func(t *testing.T) {
		n := MustParseModule(`/home/$path$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"path": Path("./foo"),
		}))
		assert.NoError(t, err)
		assert.Equal(t, Path("/home/foo"), res)
	})

	t.Run("relative path expression : interpolation value is an absolute path", func(t *testing.T) {
		n := MustParseModule(`./home/$path$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"path": Path("/foo"),
		}))
		assert.NoError(t, err)
		assert.Equal(t, Path("./home/foo"), res)
	})

	t.Run("relative path expression : interpolation value is a string", func(t *testing.T) {
		n := MustParseModule(`./home/$username$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"username": "foo",
		}))
		assert.NoError(t, err)
		assert.Equal(t, Path("./home/foo"), res)
	})

	t.Run("relative path expression : interpolation value is a string containing '/'", func(t *testing.T) {
		n := MustParseModule(`./home/$username$`)
		_, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"username": "fo/o",
		}))
		assert.Error(t, err)
	})

	t.Run("relative path expression : interpolation value is a relative path", func(t *testing.T) {
		n := MustParseModule(`./home/$path$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"path": Path("./foo"),
		}))
		assert.NoError(t, err)
		assert.Equal(t, Path("./home/foo"), res)
	})

	t.Run("relative path expression : interpolation value is an absolute path", func(t *testing.T) {
		n := MustParseModule(`./home/$path$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"path": Path("/foo"),
		}))
		assert.NoError(t, err)
		assert.Equal(t, Path("./home/foo"), res)
	})

	t.Run("HTTP host", func(t *testing.T) {
		n := MustParseModule(`https://example.com`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.Equal(t, HTTPHost("https://example.com"), res)
	})

	t.Run("HTTP host pattern", func(t *testing.T) {
		n := MustParseModule(`https://*.example.com`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.Equal(t, HTTPHostPattern("https://*.example.com"), res)
	})

	t.Run("URL expression, single path interpolation", func(t *testing.T) {
		n := MustParseModule(`https://example.com/$path$`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext(), map[string]interface{}{
			"path": "index.html",
		}))
		assert.NoError(t, err)
		assert.Equal(t, URL("https://example.com/index.html"), res)
	})

	t.Run("URL expression : host alias", func(t *testing.T) {
		n := MustParseModule(`@api/index.html`)
		ctx, _ := NewDefaultTestContext().NewWith(nil)
		ctx.addHostAlias("api", HTTPHost("https://example.com"))
		res, err := Eval(n.Statements[0], NewState(ctx))

		assert.NoError(t, err)
		assert.Equal(t, URL("https://example.com/index.html"), res)
	})
	t.Run("URL expression, query with no interpolation", func(t *testing.T) {
		n := MustParseModule(`return https://example.com/?v=a`)
		res, err := Eval(n, NewState(NewDefaultTestContext(), nil))
		assert.NoError(t, err)
		assert.Equal(t, URL("https://example.com/?v=a"), res)
	})

	t.Run("URL expression, single query interpolation", func(t *testing.T) {
		n := MustParseModule(`x = "a"; return https://example.com/?v=$x$`)
		res, err := Eval(n, NewState(NewDefaultTestContext(), nil))
		assert.NoError(t, err)
		assert.Equal(t, URL("https://example.com/?v=a"), res)
	})

	t.Run("URL expression, two query interpolations", func(t *testing.T) {
		n := MustParseModule(`x = "a"; y = "b"; return https://example.com/?v=$x$&w=$y$`)
		res, err := Eval(n, NewState(NewDefaultTestContext(), nil))
		assert.NoError(t, err)
		assert.Equal(t, URL("https://example.com/?v=a&w=b"), res)
	})

	t.Run("variable assignment", func(t *testing.T) {
		n := MustParseModule(`$a = 1; return $a`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("variable assignment (lhs: identifier literal)", func(t *testing.T) {
		n := MustParseModule(`a = 1; return $a`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("const global variable assignment", func(t *testing.T) {
		n := MustParseModule(`
			const (
				A = 1
			)

			require {
				update: {
					globals: "*"
				}
			}

			$$A = 2;
		`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("return statement : value", func(t *testing.T) {
		n := MustParseModule(`return nil`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, nil, res)
	})

	t.Run("return statement : no value", func(t *testing.T) {
		n := MustParseModule(`return`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, nil, res)
	})

	t.Run("index expression", func(t *testing.T) {
		n := MustParseModule(`$a = [0] return $a[0]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 0, res)
	})

	t.Run("element assignment", func(t *testing.T) {
		n := MustParseModule(`$a = [0] $a[0] = 1; return $a`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{1}, res)
	})

	t.Run("slice mutation", func(t *testing.T) {
		n := MustParseModule(`$a = [0] $a[0:1] = [1]; return $a`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{1}, res)
	})

	t.Run("member expression assignment : pre existing field", func(t *testing.T) {
		n := MustParseModule(`$a = {count:0}; $a.count = 1; return $a.count`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("member expression assignment : non existing field", func(t *testing.T) {
		n := MustParseModule(`$a = {}; $a.count = 1; return $a.count`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("rate literal : byte rate", func(t *testing.T) {
		n := MustParseModule(`10kB/s`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.EqualValues(t, ByteRate(10_000), res)
	})

	t.Run("rate literal : simple rate", func(t *testing.T) {
		n := MustParseModule(`10x/s`)
		res, err := Eval(n.Statements[0], NewState(NewDefaultTestContext()))
		assert.NoError(t, err)
		assert.EqualValues(t, SimpleRate(10), res)
	})

	t.Run("global constants : empty", func(t *testing.T) {
		n := MustParseModule(`
			const ()
		`)
		state := NewState(NewDefaultTestContext())
		_, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, map[string]interface{}{}, state.GlobalScope())
	})

	t.Run("global constants : single", func(t *testing.T) {
		n := MustParseModule(`
			const (
				a = 1
			)
		`)
		state := NewState(NewDefaultTestContext())
		_, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, map[string]interface{}{"a": 1}, state.GlobalScope())
	})

	t.Run("empty object literal", func(t *testing.T) {
		n := MustParseModule(`{}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{}, res)
	})

	t.Run("single prop object literal", func(t *testing.T) {
		n := MustParseModule(`{a:1}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{"a": 1}, res)
	})

	t.Run("multiprop object literal", func(t *testing.T) {
		n := MustParseModule(`{a:1,b:2}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{"a": 1, "b": 2}, res)
	})

	t.Run("object literal with only an implicit key prop", func(t *testing.T) {
		n := MustParseModule(`{:1}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{"0": 1, IMPLICIT_KEY_LEN_KEY: 1}, res)
	})

	t.Run("object literal with a spread element", func(t *testing.T) {
		n := MustParseModule(`o = {name: "foo"}; return { ...$o.{name} }`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, Object{"name": "foo"}, res)
	})

	t.Run("empty list literal", func(t *testing.T) {
		n := MustParseModule(`[]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{}, res)
	})

	t.Run("list literal : [integer]", func(t *testing.T) {
		n := MustParseModule(`[1]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1}, res)
	})

	t.Run("list literal : [integer,integer]", func(t *testing.T) {
		n := MustParseModule(`[1,2]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 2}, res)
	})

	t.Run("multi assignement", func(t *testing.T) {
		n := MustParseModule(`assign a b = [1, 2]; return [$a, $b]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 2}, res)
	})

	t.Run("if statement with true condition", func(t *testing.T) {
		n := MustParseModule(`if true { return 1 }`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("if statement with false condition", func(t *testing.T) {
		n := MustParseModule(`$a = 0; if false { $a = 1 }; return $a`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 0, res)
	})

	t.Run("if-else statement with false condition", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; if false { $a = 1 } else { $b = 1 }; return [$a, $b]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{0, 1}, res)
	})

	t.Run("for statement : empty list", func(t *testing.T) {
		n := MustParseModule(`$c = 0; for i, e in [] { $c = 1 }; return $c`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 0, res)
	})

	t.Run("for statement : single elem list", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 2; for i, e in [5] { $c1 = $i; $c2 = $e; }; return [$c1, $c2]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{0, 5}, res)
	})

	t.Run("for statement (only element variable) : single elem list", func(t *testing.T) {
		n := MustParseModule(`$c = 0; for e in [5] { $c = $e; }; return $c`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 5, res)
	})

	t.Run("for statement : two-elem list", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 0; for i, e in [5,6] { $c1 = ($c1 + $i); $c2 = ($c2 + $e); }; return [$c1, $c2]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 11}, res)
	})

	t.Run("for statement : two-elem list", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 0; for i, e in [5,6] { $c1 = ($c1 + $i); $c2 = ($c2 + $e); }; return [$c1, $c2]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 11}, res)
	})

	t.Run("for statement : integer range", func(t *testing.T) {
		n := MustParseModule(`$c1 = 0; $c2 = 0; for i, e in (5 .. 6) { $c1 = ($c1 + $i); $c2 = ($c2 + $e); }; return [$c1, $c2]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 11}, res)
	})

	t.Run("for statement : break statement", func(t *testing.T) {
		n := MustParseModule(`
			$c1 = 0; $c2 = 0; 
			for i, e in (5 .. 6) { 
				$c1 = ($c1 + $i); 
				if ($i == 1) { 
					break 
				} 
				$c2 = ($c2 + $e); 
			}; 
			return [$c1, $c2]
		`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, List{1, 5}, res)
	})

	t.Run("for <expr> statement", func(t *testing.T) {
		n := MustParseModule(`$c = 0; for (1 .. 2) { $c = ($c + 1) }; return $c`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 2, res)
	})

	t.Run("switch statement : single case (matches)", func(t *testing.T) {
		n := MustParseModule(`
			$a = 0; 
			switch 0 { 
				0 { $a = 1 } 
			}; 
			return $a
		`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("switch statement : two cases (first matches)", func(t *testing.T) {
		n := MustParseModule(`
			$a = 0; 
			$b = 0; 
			switch 0 { 
				0 { $a = 1 } 1 { $b = 1} 
			}; 
			return [$a,$b]
		`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{1, 0}, res)
	})

	t.Run("switch statement : two cases (second matches)", func(t *testing.T) {
		n := MustParseModule(`
			$a = 0; 
			$b = 0; 
			switch 1 { 
				0 { $a = 1 } 1 { $b = 1 } 
			}; 
			return [$a,$b]
		`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{0, 1}, res)
	})

	t.Run("match statement : matchers : two cases (first matches)", func(t *testing.T) {
		n := MustParseModule(`
			$a = 0; 
			$b = 0; 
			match / { 
				/* { $a = 1 } /e* { $b = 1} 
			}; 
			return [$a,$b]
		`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{1, 0}, res)
	})

	t.Run("match statement : group matchers : two cases (first matches)", func(t *testing.T) {
		n := MustParseModule(`
			$a = 0; 
			$b = 0; 
			match /home/user { 
				%/home/$username$ { $a = $username } 
				%/hom/$username$ { $b = 1} 
			}; 
			return [$a,$b]
		`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{"user", 0}, res)
	})

	t.Run("match statement : matchers : two cases (second matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; match /e { /f* { $a = 1 } /e* { $b = 1} }; return [$a,$b]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{0, 1}, res)
	})

	t.Run("match statement : equality : two cases (second matches)", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; match /e { /f* { $a = 1 } /e { $b = 1} }; return [$a,$b]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, List{0, 1}, res)
	})

	t.Run("match statement : seconde case is not a matcher nor value of the same type ", func(t *testing.T) {
		n := MustParseModule(`$a = 0; $b = 0; match /e { /f* { $a = 1 } 1 { $b = 1} }; return [$a,$b]`)
		state := NewState(NewDefaultTestContext())
		_, err := Eval(n, state)
		assert.Error(t, err)
	})

	t.Run("upper bound range expression : integer ", func(t *testing.T) {
		n := MustParseModule(`return ..10`)
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, QuantityRange{
			unknownStart: true,
			inclusiveEnd: true,
			Start:        nil,
			End:          time.Duration(10 * time.Second),
		}, res.(reflect.Value).Interface())
	})

	t.Run("rune range expression", func(t *testing.T) {
		n := MustParseModule(`'a'..'z'`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, RuneRange{'a', 'z'}, res.(reflect.Value).Interface())
	})

	t.Run("function expression : empty", func(t *testing.T) {
		n := MustParseModule(`fn(){}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)

		assert.IsType(t, &FunctionExpression{}, res)
	})

	t.Run("function declaration", func(t *testing.T) {
		n := MustParseModule(`fn f(){}`)
		state := NewState(NewDefaultTestContext())
		_, err := Eval(n, state)
		assert.NoError(t, err)

		assert.Contains(t, state.GlobalScope(), "f")
		assert.IsType(t, &FunctionDeclaration{}, state.GlobalScope()["f"])
	})

	t.Run("call declared void function", func(t *testing.T) {
		n := MustParseModule(`fn f(){  }; return f()`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, nil, res)
	})

	t.Run("call declared non-void function", func(t *testing.T) {
		n := MustParseModule(`fn f(){ return 1 }; return f()`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("call variadic Go function : arg count < non-variadic-param-count", func(t *testing.T) {
		n := MustParseModule(`gofunc()`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"gofunc": func(ctx *Context, x int, xs ...int) {},
		})
		_, err := Eval(n, state)
		assert.Error(t, err)
	})

	t.Run("call variadic Go function : arg count == non-variadic-param-count", func(t *testing.T) {
		n := MustParseModule(`gofunc(1)`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"gofunc": func(ctx *Context) (int, error) {
				return 3, nil
			},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 3, res)
	})

	t.Run("call Go function : contextless, missing permission", func(t *testing.T) {
		n := MustParseModule(`return gofunc()`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"gofunc": ctxlessFunc,
		})

		_, err := Eval(n, state)
		assert.Error(t, err)
	})

	t.Run("call Go function : contextless, granted permission", func(t *testing.T) {
		n := MustParseModule(`return gofunc()`)
		ctx, _ := NewDefaultTestContext().NewWith([]Permission{
			ContextlessCallPermission{FuncMethodName: "ctxlessFunc", ReceiverTypeName: ""},
		})
		state := NewState(ctx, map[string]interface{}{
			"gofunc": ctxlessFunc,
		})

		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 3, res)
	})

	t.Run("call Go method : contextless, missing permission", func(t *testing.T) {
		n := MustParseModule(`return gomethod()`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"gomethod": User{Name: "Foo"}.GetNameNoCtx,
		})

		_, err := Eval(n, state)
		assert.Error(t, err)
	})

	t.Run("call Go method : contextless, granted permission", func(t *testing.T) {
		n := MustParseModule(`return $$user.GetNameNoCtx()`)
		ctx, _ := NewDefaultTestContext().NewWith([]Permission{
			ContextlessCallPermission{FuncMethodName: "GetNameNoCtx", ReceiverTypeName: "User"},
		})
		state := NewState(ctx, map[string]interface{}{
			"user": User{Name: "Foo"},
		})

		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, "Foo", res)
	})

	t.Run("call Go function : interface{} returned, should be wrapped and have right type", func(t *testing.T) {
		n := MustParseModule(`
			return (getuser()).Name
		`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"getuser": func(ctx *Context) interface{} {
				return User{Name: "Foo"}
			},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, res, "Foo")
	})

	t.Run("call declared non-void function : return in if", func(t *testing.T) {
		n := MustParseModule(`fn f(){ if true { return 1 } }; return f()`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("call struct method", func(t *testing.T) {
		n := MustParseModule(`return $$user.GetName()`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"user": User{"Foo", ""},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, "Foo", res)
	})

	t.Run("call interface method", func(t *testing.T) {
		n := MustParseModule(`return $$named.GetName()`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.IsType(t, ExternalValue{}, res)
		assert.IsType(t, Object{}, res.(ExternalValue).value)
	})

	t.Run("pipeline statement", func(t *testing.T) {
		n := MustParseModule(`get-data | split-lines $`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"get-data": func(ctx *Context) string {
				return "aaa\nbbb"
			},
			"split-lines": func(ctx *Context, s string) []string {
				return strings.Split(s, "\n")
			},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, []string{"aaa", "bbb"}, UnwrapReflectVal(res))
	})

	t.Run("pipeline statement : original value of anonymous variable is restored", func(t *testing.T) {
		n := MustParseModule(`
			$ = 1
			get-data | split-lines $;
			return $
		`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"get-data": func(ctx *Context) string {
				return "aaa\nbbb"
			},
			"split-lines": func(ctx *Context, s string) []string {
				return strings.Split(s, "\n")
			},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("assignment : LHS is a pipeline expression", func(t *testing.T) {
		n := MustParseModule(`a = | get-data | split-lines $; return $a`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"get-data": func(ctx *Context) string {
				return "aaa\nbbb"
			},
			"split-lines": func(ctx *Context, s string) []string {
				return strings.Split(s, "\n")
			},
		})
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, []string{"aaa", "bbb"}, UnwrapReflectVal(res))
	})

	t.Run("member expression : <variable> <propname>", func(t *testing.T) {
		n := MustParseModule(`$a = {v: 1}; return $a.v`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("member expression : '(' <object literal> ')' <propname>", func(t *testing.T) {
		n := MustParseModule(`return ({a:1}).a`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, 1, res)
	})

	t.Run("member expression : unexported field", func(t *testing.T) {
		n := MustParseModule(`return $$val.secret`)
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"val": User{Name: "Foo", secret: "secret"},
		})
		res, err := Eval(n, state)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("extraction expression", func(t *testing.T) {
		n := MustParseModule(`return ({a:1}).{a}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, Object{"a": int(1)}, res)
	})

	t.Run("index expression : <variable> '[' 0 ']", func(t *testing.T) {
		n := MustParseModule(`$a = ["a"]; return $a[0]`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, "a", res)
	})

	t.Run("key list expression : empty", func(t *testing.T) {
		n := MustParseModule(`return .{}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, KeyList{}, res)
	})

	t.Run("key list expression : single element", func(t *testing.T) {
		n := MustParseModule(`return .{name}`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.Equal(t, KeyList{"name"}, res)
	})

	t.Run("lazy expression : @ <integer>", func(t *testing.T) {
		n := MustParseModule(`@(1)`)
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n.Statements[0], state)
		assert.NoError(t, err)
		assert.EqualValues(t, &IntLiteral{
			NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
			Raw:      "1",
			Value:    1,
		}, res)
	})

	t.Run("import statement : no globals, no required permissions", func(t *testing.T) {
		n := MustParseModule(strings.ReplaceAll(`
			import importname https://modules.com/return_1.gos "<hash>" {} allow {}
			return $$importname
		`, "<hash>", RETURN_1_MODULE_HASH))
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("import statement : imported module returns $$a", func(t *testing.T) {
		n := MustParseModule(strings.ReplaceAll(`
			import importname https://modules.com/return_global_a.gos "<hash>" {a: 1} allow {read: {globals: "a"}}
			return $$importname
		`, "<hash>", RETURN_GLOBAL_A_MODULE_HASH))
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.EqualValues(t, 1, res)
	})

	t.Run("spawn expression : no globals, empty embedded module", func(t *testing.T) {
		n := MustParseModule(`
			sr nil { }
		`)
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
		_, err := Eval(n, state)
		assert.NoError(t, err)
	})

	t.Run("spawn expression : no globals, group (used once)", func(t *testing.T) {
		n := MustParseModule(`
			sr group nil { }

			return $group
		`)
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext(), map[string]interface{}{
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
		state := NewState(NewDefaultTestContext())
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
		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)
		assert.NoError(t, err)
		assert.IsType(t, ExternalValue{}, res)
		assert.Equal(t, Object{}, res.(ExternalValue).value)
	})

	t.Run("a value passed to a routine and then returned by it should not be wrapped", func(t *testing.T) {
		called := false

		n := MustParseModule(`
			$rt = sr {gofunc: $$gofunc} {
				return $$gofunc
			}

			$f = $rt.WaitResult()!
			return $f()
		`)

		_ctx := NewDefaultTestContext()
		state := NewState(_ctx, map[string]interface{}{
			"gofunc": func(ctx *Context) int {
				called = true

				if ctx != _ctx {
					t.Fatal("the context should be the main one")
				}
				return 0
			},
		})
		_, err := Eval(n, state)
		assert.True(t, called)
		assert.NoError(t, err)
	})

	t.Run("dropped permissions", func(t *testing.T) {
		n := MustParseModule(`
			drop-perms {
				read: {
					globals: "*"
				}
			}
		`)

		state := NewState(NewDefaultTestContext())
		_, err := Eval(n, state)

		assert.True(t, state.ctx.HasPermission(GlobalVarPermission{Kind_: UsePerm, Name: "*"}))
		assert.False(t, state.ctx.HasPermission(GlobalVarPermission{Kind_: ReadPerm, Name: "*"}))

		assert.NoError(t, err)
	})

	t.Run("boolean conversion expression", func(t *testing.T) {
		n := MustParseModule(`$$invalid?`)

		state := NewState(NewDefaultTestContext(), map[string]interface{}{
			"invalid": reflect.ValueOf(nil),
		})
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.Equal(t, false, res)
	})

	t.Run("pattern definition : identifier : RHS is a string literal", func(t *testing.T) {
		n := MustParseModule(`%s = "s"; return %s`)

		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.Equal(t, ExactSimpleValueMatcher{"s"}, res)
	})

	t.Run("pattern definition & identifiers : RHS is another pattern identifier", func(t *testing.T) {
		n := MustParseModule(`%p = "p"; %s = %p; return %s`)

		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.Equal(t, ExactSimpleValueMatcher{"p"}, res)
	})

	t.Run("object pattern literal : empty", func(t *testing.T) {
		n := MustParseModule(`%{}`)

		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.Equal(t, &ObjectPattern{
			EntryMatchers: map[string]Matcher{},
		}, res)
	})

	t.Run("object pattern literal : not empty", func(t *testing.T) {
		n := MustParseModule(`%s = "s"; return %{name: %s, count: 2}`)

		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.Equal(t, &ObjectPattern{
			EntryMatchers: map[string]Matcher{
				"name":  ExactSimpleValueMatcher{"s"},
				"count": ExactSimpleValueMatcher{int(2)},
			},
		}, res)
	})

	t.Run("list pattern literal : empty", func(t *testing.T) {
		n := MustParseModule(`%[]`)

		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.Equal(t, &ListPattern{
			ElementMatchers: make([]Matcher, 0),
		}, res)
	})

	t.Run("list pattern literal : not empty", func(t *testing.T) {
		n := MustParseModule(`%[ 2 ]`)

		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.Equal(t, &ListPattern{
			ElementMatchers: []Matcher{
				ExactSimpleValueMatcher{int(2)},
			},
		}, res)
	})

	t.Run("regex literal : empty", func(t *testing.T) {
		n := MustParseModule(`%"a"`)

		state := NewState(NewDefaultTestContext())
		res, err := Eval(n, state)

		assert.NoError(t, err)
		assert.IsType(t, RegexMatcher{}, res)
	})

}

func TestHttpPermission(t *testing.T) {

	ENTITIES := List{
		URL("https://localhost:443/?a=1"),
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

func TestContextlessCallPermission(t *testing.T) {

	funCallPerm := ContextlessCallPermission{FuncMethodName: "f", ReceiverTypeName: ""}
	funCallPerm2 := ContextlessCallPermission{FuncMethodName: "g", ReceiverTypeName: ""}
	methodCallPerm := ContextlessCallPermission{FuncMethodName: "f", ReceiverTypeName: "User"}

	assert.True(t, funCallPerm.Includes(funCallPerm))
	assert.True(t, methodCallPerm.Includes(methodCallPerm))

	assert.False(t, methodCallPerm.Includes(funCallPerm))
	assert.False(t, funCallPerm.Includes(methodCallPerm))
	assert.False(t, funCallPerm.Includes(funCallPerm2))
	assert.False(t, funCallPerm2.Includes(funCallPerm))
}

func TestForbiddenPermissions(t *testing.T) {

	readGoFiles := FilesystemPermission{ReadPerm, PathPattern("./*.go")}
	readFile := FilesystemPermission{ReadPerm, Path("./file.go")}

	ctx := NewContext([]Permission{readGoFiles}, []Permission{readFile}, nil)

	assert.True(t, ctx.HasPermission(readGoFiles))
	assert.False(t, ctx.HasPermission(readFile))
}

func TestDropPermissions(t *testing.T) {
	readGoFiles := FilesystemPermission{ReadPerm, PathPattern("./*.go")}
	readFile := FilesystemPermission{ReadPerm, Path("./file.go")}

	ctx := NewContext([]Permission{readGoFiles}, []Permission{readFile}, nil)

	ctx.DropPermissions([]Permission{readGoFiles})

	assert.False(t, ctx.HasPermission(readGoFiles))
	assert.False(t, ctx.HasPermission(readFile))
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
		}, nil, nil))
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
		}, nil, nil))
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

	t.Run("byte rate", func(t *testing.T) {
		ctx := NewContext(nil, nil, []Limitation{
			{Name: "fs/read", ByteRate: 1_000},
		})

		start := time.Now()

		//BYTE RATE

		//should not cause a wait
		ctx.Take("fs/read", 1_000)
		assert.WithinDuration(t, start, time.Now(), time.Millisecond)

		expectedTime := time.Now().Add(time.Second)

		//should cause a wait
		ctx.Take("fs/read", 1_000)
		assert.WithinDuration(t, expectedTime, time.Now(), 200*time.Millisecond)
	})

	t.Run("simple rate", func(t *testing.T) {
		ctx := NewContext(nil, nil, []Limitation{
			{Name: "fs/read-file", SimpleRate: 1},
		})

		start := time.Now()
		expectedTime := start.Add(time.Second)

		ctx.Take("fs/read-file", 1)
		assert.WithinDuration(t, start, time.Now(), time.Millisecond)

		//should cause a wait
		ctx.Take("fs/read-file", 1)
		assert.WithinDuration(t, expectedTime, time.Now(), 200*time.Millisecond)
	})

	t.Run("total", func(t *testing.T) {
		ctx := NewContext(nil, nil, []Limitation{
			{Name: "fs/total-read-file", Total: 1},
		})

		ctx.Take("fs/total-read-file", 1)

		assert.Panics(t, func() {
			ctx.Take("fs/total-read-file", 1)
		})
	})

	t.Run("auto decrement", func(t *testing.T) {
		ctx := NewContext(nil, nil, []Limitation{
			{
				Name:  "test",
				Total: int64(time.Second),
				DecrementFn: func(lastDecrementTime time.Time) int64 {
					v := TOKEN_BUCKET_CAPACITY_SCALE * time.Since(lastDecrementTime)
					return v.Nanoseconds()
				},
			},
		})

		capacity := int64(time.Second * TOKEN_BUCKET_CAPACITY_SCALE)

		assert.Equal(t, capacity, ctx.limiters["test"].bucket.avail)
		time.Sleep(time.Second)
		assert.InDelta(t, int64(0), ctx.limiters["test"].bucket.avail, float64(capacity/20))
	})

}

func TestToBool(t *testing.T) {

	testCases := []struct {
		name  string
		input interface{}
		ok    bool
	}{
		{"nil slice", ([]int)(nil), false},
		{"empty, not-nil slice", []int{}, false},
		{"not empty slice", []int{2}, true},
		{"not empty pointer", &User{}, true},
		{"empty pointer", (*User)(nil), false},
		{"unitialized struct", User{}, true},
		{"empty string", "", false},
		{"not empty string", "1", true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.True(t, testCase.ok == toBool(ToReflectVal(testCase.input)))
		})
	}
}

func TestPathPatternTest(t *testing.T) {
	assert.True(t, PathPattern("/*").Test(Path("/")))
	assert.True(t, PathPattern("/*").Test(Path("/e")))
	assert.False(t, PathPattern("/*").Test(Path("/e/")))
	assert.False(t, PathPattern("/*").Test(Path("/e/e")))
}

func TestNamedSegmentPathPatternTest(t *testing.T) {

	res := parseEval(t, `%/home/$username$`)
	patt := res.(NamedSegmentPathPattern)

	for _, testCase := range []struct {
		ok   bool
		path Path
	}{
		{false, "/home"},
		{false, "/home/"},
		{true, "/home/user"},
		{false, "/home/user/"},
		{false, "/home/user/e"},
	} {
		t.Run(string(testCase.path), func(t *testing.T) {
			assert.Equal(t, testCase.ok, patt.Test(testCase.path))
		})
	}
}

func TestNamedSegmentPathPatternMatchGroups(t *testing.T) {
	res1 := parseEval(t, `%/home/$username$`)
	patt1 := res1.(NamedSegmentPathPattern)

	for _, testCase := range []struct {
		groups map[string]interface{}
		path   Path
	}{
		{nil, "/home"},
		{nil, "/home/"},
		{map[string]interface{}{"username": "user"}, "/home/user"},
		{nil, "/home/user/"},
		{nil, "/home/user/e"},
	} {
		t.Run(string(testCase.path), func(t *testing.T) {
			ok, groups := patt1.MatchGroups(testCase.path)
			if ok != (testCase.groups != nil) {
				assert.FailNow(t, "invalid match")
			}
			assert.Equal(t, testCase.groups, groups)
		})

	}

	res2 := parseEval(t, `%/home/$username$/`)
	patt2 := res2.(NamedSegmentPathPattern)

	for _, testCase := range []struct {
		groups map[string]interface{}
		path   Path
	}{
		{nil, "/home"},
		{nil, "/home/"},
		{nil, "/home/user"},
		{map[string]interface{}{"username": "user"}, "/home/user/"},
		{nil, "/home/user/e"},
	} {
		t.Run("pattern ends with slash, "+string(testCase.path), func(t *testing.T) {
			ok, groups := patt2.MatchGroups(testCase.path)
			if ok != (testCase.groups != nil) {
				assert.FailNow(t, "invalid match")
			}
			assert.Equal(t, testCase.groups, groups)
		})

	}
}

func TestCompileStringPatternNode(t *testing.T) {

	t.Run("single element : string literal", func(t *testing.T) {
		ctx := NewContext(nil, nil, nil)
		ctx.addNamedPattern("s", ExactSimpleValueMatcher{"s"})
		state := NewState(ctx)

		patt, err := CompileStringPatternNode(&PatternPiece{
			Kind: StringPattern,
			Elements: []*PatternPieceElement{
				{
					Ocurrence: ExactlyOneOcurrence,
					Expr:      &StringLiteral{Value: "s"},
				},
			},
		}, state)

		assert.NoError(t, err)
		assert.Equal(t, "(s)", patt.Regex())
	})

	t.Run("single element : rune range expression", func(t *testing.T) {
		ctx := NewContext(nil, nil, nil)
		state := NewState(ctx)

		patt, err := CompileStringPatternNode(&PatternPiece{
			Kind: StringPattern,
			Elements: []*PatternPieceElement{
				{
					Ocurrence: ExactlyOneOcurrence,
					Expr: &RuneRangeExpression{
						Lower: &RuneLiteral{Value: 'a'},
						Upper: &RuneLiteral{Value: 'z'},
					},
				},
			},
		}, state)

		assert.NoError(t, err)
		assert.Equal(t, "([a-z])", patt.Regex())
	})

	t.Run("single element : string literal (ocurrence modifier i '*')", func(t *testing.T) {
		ctx := NewContext(nil, nil, nil)
		ctx.addNamedPattern("s", ExactSimpleValueMatcher{"s"})
		state := NewState(ctx)

		patt, err := CompileStringPatternNode(&PatternPiece{
			Kind: StringPattern,
			Elements: []*PatternPieceElement{
				{
					Ocurrence: ZeroOrMoreOcurrence,
					Expr:      &StringLiteral{Value: "s"},
				},
			},
		}, state)

		assert.NoError(t, err)
		assert.Equal(t, "(s)*", patt.Regex())
	})

	t.Run("single element : string literal (ocurrence modifier i '=' 2)", func(t *testing.T) {
		ctx := NewContext(nil, nil, nil)
		ctx.addNamedPattern("s", ExactSimpleValueMatcher{"s"})
		state := NewState(ctx)

		patt, err := CompileStringPatternNode(&PatternPiece{
			Kind: StringPattern,
			Elements: []*PatternPieceElement{
				{
					Ocurrence:           ExactOcurrence,
					ExactOcurrenceCount: 2,
					Expr:                &StringLiteral{Value: "s"},
				},
			},
		}, state)

		assert.NoError(t, err)
		assert.Equal(t, "(s){2}", patt.Regex())
	})

	t.Run("two elements : one string literal + a pattern identifier (exact string matcher)", func(t *testing.T) {
		ctx := NewContext(nil, nil, nil)
		ctx.addNamedPattern("b", ExactSimpleValueMatcher{"c"})
		state := NewState(ctx)

		patt, err := CompileStringPatternNode(&PatternPiece{
			Kind: StringPattern,
			Elements: []*PatternPieceElement{
				{
					Ocurrence: ExactlyOneOcurrence,
					Expr:      &StringLiteral{Value: "a"},
				},
				{
					Ocurrence: ExactlyOneOcurrence,
					Expr:      &PatternIdentifierLiteral{Name: "b"},
				},
			},
		}, state)

		assert.NoError(t, err)
		assert.Equal(t, "(a)(c)", patt.Regex())
	})

	t.Run("union of two single-element cases", func(t *testing.T) {
		ctx := NewContext(nil, nil, nil)
		state := NewState(ctx)

		patt, err := CompileStringPatternNode(&PatternUnion{
			Cases: []Node{
				&StringLiteral{Value: "a"},
				&StringLiteral{Value: "b"},
			},
		}, state)

		assert.NoError(t, err)
		assert.Equal(t, "(a|b)", patt.Regex())
	})

	t.Run("union of two multiple-element cases", func(t *testing.T) {
		ctx := NewContext(nil, nil, nil)
		state := NewState(ctx)

		patt, err := CompileStringPatternNode(&PatternUnion{
			Cases: []Node{
				&PatternPiece{
					Kind: StringPattern,
					Elements: []*PatternPieceElement{
						{
							Ocurrence: ExactlyOneOcurrence,
							Expr:      &StringLiteral{Value: "a"},
						},
						{
							Ocurrence: ExactlyOneOcurrence,
							Expr:      &StringLiteral{Value: "b"},
						},
					},
				},

				&PatternPiece{
					Kind: StringPattern,
					Elements: []*PatternPieceElement{
						{
							Ocurrence: ExactlyOneOcurrence,
							Expr:      &StringLiteral{Value: "c"},
						},
						{
							Ocurrence: ExactlyOneOcurrence,
							Expr:      &StringLiteral{Value: "d"},
						},
					},
				},
			},
		}, state)

		assert.NoError(t, err)
		assert.Equal(t, "((a)(b)|(c)(d))", patt.Regex())
	})
}

func TestRepeatedPatternElementRandom(t *testing.T) {

	t.Run("2 ocurrences of constant string", func(t *testing.T) {
		patt := RepeatedPatternElement{
			regexp:            nil,
			ocurrenceModifier: ExactOcurrence,
			exactCount:        2,
			element:           ExactSimpleValueMatcher{"a"},
		}

		for i := 0; i < 5; i++ {
			assert.Equal(t, "aa", patt.Random().(string))
		}
	})

	t.Run("optional ocurrence of constant string", func(t *testing.T) {
		patt := RepeatedPatternElement{
			regexp:            nil,
			ocurrenceModifier: OptionalOcurrence,
			element:           ExactSimpleValueMatcher{"a"},
		}

		for i := 0; i < 5; i++ {
			s := patt.Random().(string)
			assert.Equal(t, strings.Repeat("a", len(s)), s)
		}
	})

	t.Run("zero or more ocurrences of constant string", func(t *testing.T) {
		patt := RepeatedPatternElement{
			regexp:            nil,
			ocurrenceModifier: ZeroOrMoreOcurrence,
			element:           ExactSimpleValueMatcher{"a"},
		}

		for i := 0; i < 5; i++ {
			s := patt.Random().(string)
			length := len(s)

			assert.Equal(t, strings.Repeat("a", length), s)
		}
	})

	t.Run("at least one ocurrence of constant string", func(t *testing.T) {
		patt := RepeatedPatternElement{
			regexp:            nil,
			ocurrenceModifier: ZeroOrMoreOcurrence,
			element:           ExactSimpleValueMatcher{"a"},
		}

		for i := 0; i < 5; i++ {
			s := patt.Random().(string)
			length := len(s)

			assert.Equal(t, strings.Repeat("a", length), s)
		}
	})
}

func TestSequenceStringPatternRandom(t *testing.T) {

	patt1 := SequenceStringPattern{
		regexp: nil,
		node:   nil,
		elements: []StringPatternElement{
			ExactSimpleValueMatcher{"a"},
			ExactSimpleValueMatcher{"b"},
		},
	}

	assert.Equal(t, "ab", patt1.Random())
}

func TestUnionStringPatternRandom(t *testing.T) {

	patt1 := UnionStringPattern{
		regexp: nil,
		node:   nil,
		cases: []StringPatternElement{
			ExactSimpleValueMatcher{"a"},
			ExactSimpleValueMatcher{"b"},
		},
	}

	for i := 0; i < 5; i++ {
		s := patt1.Random().(string)
		assert.True(t, s == "a" || s == "b")
	}

}

func TestShiftNodeSpans(t *testing.T) {

	node := &Module{
		NodeBase: NodeBase{NodeSpan{0, 2}, nil, nil},
		Statements: []Node{
			&IntLiteral{
				NodeBase: NodeBase{NodeSpan{0, 1}, nil, nil},
			},
		},
	}

	shiftNodeSpans(node, +2)
	assert.EqualValues(t, &Module{
		NodeBase: NodeBase{NodeSpan{2, 4}, nil, nil},
		Statements: []Node{
			&IntLiteral{
				NodeBase: NodeBase{NodeSpan{2, 3}, nil, nil},
			},
		},
	}, node)

}

type inMemoryKv struct {
	data map[string]interface{}
	lock sync.Mutex
}

func (kv *inMemoryKv) Get(ctx *Context, key string) (interface{}, bool) {
	kv.lock.Lock()
	defer kv.lock.Unlock()
	v, ok := kv.data[key]
	return v, ok
}

func (kv *inMemoryKv) Set(ctx *Context, key string, value interface{}) {
	kv.lock.Lock()
	defer kv.lock.Unlock()
	kv.data[key] = value
}

func (kv *inMemoryKv) Has(ctx *Context, key string) bool {
	kv.lock.Lock()
	defer kv.lock.Unlock()

	_, ok := kv.data[key]
	return ok
}

func (kv *inMemoryKv) Lock() {
	kv.lock.Lock()
}

func (kv *inMemoryKv) Unlock() {
	kv.lock.Unlock()
}

func TestCookieJar(t *testing.T) {

	const PROFILE_1_NAME = "user"
	const HOST_1 = "localhost"

	URL_1, _ := url.Parse("https://localhost/")
	URL_2, _ := url.Parse("https://localhost:8080/")

	makeNewContext := func() *Context {
		return NewContext([]Permission{
			GlobalVarPermission{ReadPerm, "*"},
			GlobalVarPermission{UsePerm, "*"},
		}, nil, nil)

	}

	t.Run("set a cookie", func(t *testing.T) {
		kv := &inMemoryKv{data: map[string]interface{}{}}
		ctx := makeNewContext()
		jar, _ := newCookieJar(ctx, PROFILE_1_NAME, kv)

		cookies := []*http.Cookie{{Name: "a", Value: "0"}}
		jar.SetCookies(URL_1, cookies)
		assert.EqualValues(t, cookies, jar.Cookies(URL_1))

		data, ok := kv.Get(nil, COOKIE_KV_KEY)
		assert.True(t, ok)

		assert.EqualValues(t, map[string]interface{}{
			PROFILE_1_NAME: map[string]interface{}{
				HOST_1: []interface{}{_toUnstructured(http.Cookie{Name: "a", Value: "0"})},
			},
		}, data)

	})

	t.Run("set two cookies at the same URL, one after another", func(t *testing.T) {
		kv := &inMemoryKv{data: map[string]interface{}{}}
		ctx := makeNewContext()
		jar, _ := newCookieJar(ctx, PROFILE_1_NAME, kv)

		cookie1 := &http.Cookie{Name: "a", Value: "0"}
		cookie2 := &http.Cookie{Name: "b", Value: "1"}

		jar.SetCookies(URL_1, []*http.Cookie{cookie1})
		jar.SetCookies(URL_1, []*http.Cookie{cookie2})

		assert.EqualValues(t, []*http.Cookie{cookie1, cookie2}, jar.Cookies(URL_1))

		data, ok := kv.Get(nil, COOKIE_KV_KEY)
		assert.True(t, ok)

		assert.EqualValues(t, map[string]interface{}{
			PROFILE_1_NAME: map[string]interface{}{
				HOST_1: []interface{}{
					_toUnstructured(http.Cookie{Name: "a", Value: "0"}),
					_toUnstructured(http.Cookie{Name: "b", Value: "1"}),
				},
			},
		}, data)

	})

	t.Run("set two cookies at two diffents origins, one after another", func(t *testing.T) {
		kv := &inMemoryKv{data: map[string]interface{}{}}
		ctx := makeNewContext()
		jar, _ := newCookieJar(ctx, PROFILE_1_NAME, kv)

		cookie1 := &http.Cookie{Name: "a", Value: "0"}
		cookie2 := &http.Cookie{Name: "b", Value: "1"}

		jar.SetCookies(URL_1, []*http.Cookie{cookie1})
		jar.SetCookies(URL_2, []*http.Cookie{cookie2})

		assert.EqualValues(t, []*http.Cookie{cookie1, cookie2}, jar.Cookies(URL_1))
		assert.EqualValues(t, []*http.Cookie{cookie1, cookie2}, jar.Cookies(URL_2))

		data, ok := kv.Get(nil, COOKIE_KV_KEY)
		assert.True(t, ok)

		assert.EqualValues(t, map[string]interface{}{
			PROFILE_1_NAME: map[string]interface{}{
				HOST_1: []interface{}{
					_toUnstructured(http.Cookie{Name: "a", Value: "0"}),
					_toUnstructured(http.Cookie{Name: "b", Value: "1"}),
				},
			},
		}, data)

	})

	t.Run("set two cookies at the same URL at the same time", func(t *testing.T) {
		kv := &inMemoryKv{data: map[string]interface{}{}}
		ctx := makeNewContext()
		jar, _ := newCookieJar(ctx, PROFILE_1_NAME, kv)

		cookies := []*http.Cookie{{Name: "a", Value: "0"}, {Name: "b", Value: "1"}}
		jar.SetCookies(URL_1, cookies)
		assert.EqualValues(t, cookies, jar.Cookies(URL_1))

		data, ok := kv.Get(nil, COOKIE_KV_KEY)
		assert.True(t, ok)

		assert.EqualValues(t, map[string]interface{}{
			PROFILE_1_NAME: map[string]interface{}{
				HOST_1: []interface{}{
					_toUnstructured(http.Cookie{Name: "a", Value: "0"}),
					_toUnstructured(http.Cookie{Name: "b", Value: "1"}),
				},
			},
		}, data)

	})

	t.Run("init cookie jar with non-empty KV store and add a new cookie at the same URL", func(t *testing.T) {
		kv := &inMemoryKv{data: map[string]interface{}{
			COOKIE_KV_KEY: map[string]interface{}{
				PROFILE_1_NAME: map[string]interface{}{
					HOST_1: []interface{}{_toUnstructured(http.Cookie{Name: "a", Value: "0"})},
				},
			},
		}}
		ctx := makeNewContext()
		jar, _ := newCookieJar(ctx, PROFILE_1_NAME, kv)

		assert.EqualValues(t, []*http.Cookie{{Name: "a", Value: "0"}}, jar.Cookies(URL_1))

		//ADD NEW COOKIE
		jar.SetCookies(URL_1, []*http.Cookie{{Name: "b", Value: "1"}})
		assert.EqualValues(t, []*http.Cookie{{Name: "a", Value: "0"}, {Name: "b", Value: "1"}}, jar.Cookies(URL_1))

		data, ok := kv.Get(nil, COOKIE_KV_KEY)
		assert.True(t, ok)

		assert.EqualValues(t, map[string]interface{}{
			PROFILE_1_NAME: map[string]interface{}{
				HOST_1: []interface{}{
					_toUnstructured(http.Cookie{Name: "a", Value: "0"}),
					_toUnstructured(http.Cookie{Name: "b", Value: "1"}),
				},
			},
		}, data)

	})
}
