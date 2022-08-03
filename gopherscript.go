package gopherscript

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const TRULY_MAX_STACK_HEIGHT = 10
const DEFAULT_MAX_STACK_HEIGHT = 5
const MAX_OBJECT_KEY_BYTE_LEN = 64
const MAX_PATTERN_OCCURRENCE_COUNT = 1 << 24
const HTTP_URL_PATTERN = "^https?:\\/\\/(localhost|(www\\.)?[-a-zA-Z0-9@:%._+~#=]{1,32}\\.[a-zA-Z0-9]{1,6})\\b([-a-zA-Z0-9@:%_+.~#?&//=]{0,100})$"
const LOOSE_URL_EXPR_PATTERN = "^(@[a-zA-Z0-9_-]+|https?:\\/\\/(localhost|(www\\.)?[-a-zA-Z0-9@:%._+~#=]{1,32}\\.[a-zA-Z0-9]{1,6})\\b)([-a-zA-Z0-9@:%_+.~#?&//=$]{0,100})$"
const LOOSE_HTTP_HOST_PATTERN_PATTERN = "^https?:\\/\\/(\\*|(www\\.)?[-a-zA-Z0-9.*]{1,32}\\.[a-zA-Z0-9*]{1,6})(:[0-9]{1,5})?$"
const IMPLICIT_KEY_LEN_KEY = "__len"
const GOPHERSCRIPT_MIMETYPE = "application/gopherscript"
const RETURN_1_MODULE_HASH = "SG2a/7YNuwBjsD2OI6bM9jZM4gPcOp9W8g51DrQeyt4="
const RETURN_GLOBAL_A_MODULE_HASH = "UYvV2gLwfuQ2D91v7PzQ8RMugUTcM0lOysCMqMqXfmg"
const TOKEN_BUCKET_CAPACITY_SCALE = 100
const TOKEN_BUCKET_INTERVAL = time.Second / TOKEN_BUCKET_CAPACITY_SCALE

const EXECUTION_TOTAL_LIMIT_NAME = "execution/total-time"
const COMPUTE_TIME_TOTAL_LIMIT_NAME = "execution/total-compute-time"
const IO_TIME_TOTAL_LIMIT_NAME = "execution/total-io-time"

var HTTP_URL_REGEX = regexp.MustCompile(HTTP_URL_PATTERN)
var LOOSE_HTTP_HOST_PATTERN_REGEX = regexp.MustCompile(LOOSE_HTTP_HOST_PATTERN_PATTERN)
var LOOSE_URL_EXPR_PATTERN_REGEX = regexp.MustCompile(LOOSE_URL_EXPR_PATTERN)
var isSpace = regexp.MustCompile(`^\s+`).MatchString
var KEYWORDS = []string{"if", "else", "require", "drop-perms", "for", "assign", "const", "fn", "switch", "match", "import", "sr", "return", "break", "continue"}
var REQUIRE_KEYWORD_STR = "require"
var CONST_KEYWORD_STR = "const"
var PERMISSION_KIND_STRINGS = []string{"read", "update", "create", "delete", "use", "consume", "provide"}

var CTX_PTR_TYPE = reflect.TypeOf(&Context{})
var ERROR_INTERFACE_TYPE = reflect.TypeOf((*error)(nil)).Elem()
var ITERABLE_INTERFACE_TYPE = reflect.TypeOf((*Iterable)(nil)).Elem()
var UINT8_SLICE_TYPE = reflect.TypeOf(([]uint8)(nil)).Elem()
var MODULE_CACHE = map[string]string{
	RETURN_1_MODULE_HASH:        "return 1",
	RETURN_GLOBAL_A_MODULE_HASH: "return $$a",
}

func isKeyword(str string) bool {
	return strSliceContains(KEYWORDS, str)
}

func strSliceContains(strings []string, str string) bool {
	for _, e := range strings {
		if e == str {
			return true
		}
	}

	return false
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isIdentChar(r rune) bool {
	return isAlpha(r) || isDigit(r) || r == '-' || r == '_'
}

func isInterpolationAllowedChar(r rune) bool {
	return isIdentChar(r) || isDigit(r) || r == '[' || r == ']' || r == '.' || r == '$'
}

func isDelim(r rune) bool {
	switch r {
	case '{', '}', '[', ']', '(', ')', ',', ';', ':', '|':
		return true
	default:
		return false
	}
}

func isNotPairedOrIsClosingDelim(r rune) bool {
	switch r {
	case ',', ';', ':', ')', ']', '}', '|':
		return true
	default:
		return false
	}
}

func HasPathLikeStart(s string) bool {
	if len(s) == 0 {
		return false
	}

	return s[0] == '/' || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

//all node types embed NodeBase, NodeBase implements the Node interface
type Node interface {
	Base() NodeBase
	BasePtr() *NodeBase
}

type Statement interface {
	Node
}

type NodeSpan struct {
	Start int
	End   int
}

type ValuelessTokenType int

const (
	IF_KEYWORD ValuelessTokenType = iota
	ELSE_KEYWORD
	REQUIRE_KEYWORD
	DROP_PERMS_KEYWORD
	ASSIGN_KEYWORD
	CONST_KEYWORD
	FOR_KEYWORD
	IN_KEYWORD
	SPAWN_KEYWORD
	ALLOW_KEYWORD
	IMPORT_KEYWORD
	FN_KEYWORD
	SWITCH_KEYWORD
	MATCH_KEYWORD
	RETURN_KEYWORD
	BREAK_KEYWORD
	CONTINUE_KEYWORD
	OPENING_BRACKET
	CLOSING_BRACKET
	OPENING_CURLY_BRACKET
	CLOSING_CURLY_BRACKET
	OPENING_PARENTHESIS
	CLOSING_PARENTHESIS
	COMMA
	COLON
	SEMICOLON
)

type ValuelessToken struct {
	Type ValuelessTokenType
	Span NodeSpan
}

type NodeBase struct {
	Span            NodeSpan
	Err             *ParsingError
	ValuelessTokens []ValuelessToken
}

func (base NodeBase) Base() NodeBase {
	return base
}

func (base *NodeBase) BasePtr() *NodeBase {
	return base
}

func (base NodeBase) IncludedIn(node Node) bool {
	return base.Span.Start >= node.Base().Span.Start && base.Span.End <= node.Base().Span.End
}

type InvalidURLPattern struct {
	NodeBase
	Value string
}

type InvalidURL struct {
	NodeBase
	Value string
}

type InvalidAliasRelatedNode struct {
	NodeBase
	Value string
}

type InvalidComplexPatternElement struct {
	NodeBase
}

type InvalidObjectElement struct {
	NodeBase
}

type InvalidMemberLike struct {
	NodeBase
	Left  Node
	Right Node //can be nil
}

type InvalidPathSlice struct {
	NodeBase
}

type MissingExpression struct {
	NodeBase
}

type UnknownNode struct {
	NodeBase
}

func isScopeContainerNode(node Node) bool {
	switch node.(type) {
	case *Module, *EmbeddedModule, *FunctionExpression, *LazyExpression:
		return true
	default:
		return false
	}
}

type Module struct {
	NodeBase
	GlobalConstantDeclarations *GlobalConstantDeclarations //nil if no const declarations at the top of the module
	Requirements               *Requirements               //nil if no require at the top of the module
	Statements                 []Node
}

type EmbeddedModule struct {
	NodeBase
	Requirements *Requirements
	Statements   []Node
}

type Variable struct {
	NodeBase
	Name string
}

type GlobalVariable struct {
	NodeBase
	Name string
}

type MemberExpression struct {
	NodeBase
	Left         Node
	PropertyName *IdentifierLiteral
}

type IdentifierMemberExpression struct {
	NodeBase
	Left          *IdentifierLiteral
	PropertyNames []*IdentifierLiteral
}

type IndexExpression struct {
	NodeBase
	Indexed Node
	Index   Node
}

type SliceExpression struct {
	NodeBase
	Indexed    Node
	StartIndex Node //can be nil
	EndIndex   Node //can be nil
}

type KeyListExpression struct {
	NodeBase
	Keys []*IdentifierLiteral
}

type BooleanConversionExpression struct {
	NodeBase
	Expr Node
}

type BooleanLiteral struct {
	NodeBase
	Value bool
}

type FlagLiteral struct {
	NodeBase
	SingleDash bool
	Name       string
}

type OptionExpression struct {
	NodeBase
	SingleDash bool
	Name       string
	Value      Node
}

type IntLiteral struct {
	NodeBase
	Raw   string
	Value int
}

type FloatLiteral struct {
	NodeBase
	Raw   string
	Value float64
}

type QuantityLiteral struct {
	NodeBase
	Raw   string
	Value float64
	Unit  string
}

type RateLiteral struct {
	NodeBase
	Quantity *QuantityLiteral
	Unit     *IdentifierLiteral
}

type RuneLiteral struct {
	NodeBase
	Value rune
}

type StringLiteral struct {
	NodeBase
	Raw   string
	Value string
}

type URLLiteral struct {
	NodeBase
	Value string
}

type HTTPHostLiteral struct {
	NodeBase
	Value string
}

type HTTPHostPatternLiteral struct {
	NodeBase
	Value string
}

type URLPatternLiteral struct {
	NodeBase
	Value string
}

type AtHostLiteral struct {
	NodeBase
	Value string
}

type AbsolutePathLiteral struct {
	NodeBase
	Value string
}

type RelativePathLiteral struct {
	NodeBase
	Value string
}

type AbsolutePathPatternLiteral struct {
	NodeBase
	Value string
}

type RelativePathPatternLiteral struct {
	NodeBase
	Value string
}

//TODO: rename
type NamedSegmentPathPatternLiteral struct {
	NodeBase
	Slices []Node //PathSlice | Variable
}

type RelativePathExpression struct {
	NodeBase
	Slices []Node
}

type AbsolutePathExpression struct {
	NodeBase
	Slices []Node
}

type RegularExpressionLiteral struct {
	NodeBase
	Raw   string
	Value string
}

type URLExpression struct {
	NodeBase
	Raw         string
	HostPart    Node
	Path        *AbsolutePathExpression
	QueryParams []Node
}

type URLQueryExpression struct {
	NodeBase
	Parameters map[string][]Node
}

type URLQueryParameter struct {
	NodeBase
	Name  string
	Value []Node
}

type URLQueryParameterSlice struct {
	NodeBase
	Value string
}

type PathSlice struct {
	NodeBase
	Value string
}

type NilLiteral struct {
	NodeBase
}

type ObjectLiteral struct {
	NodeBase
	Properties     []ObjectProperty
	SpreadElements []*PropertySpreadElement
}

type ExtractionExpression struct {
	NodeBase
	Object Node
	Keys   *KeyListExpression
}

type PropertySpreadElement struct {
	NodeBase
	Extraction *ExtractionExpression
}

func getCommandPermissions(n Node) ([]Permission, error) {

	var perms []Permission

	ERR_PREFIX := "invalid requirements, use: commands: "
	ERR := ERR_PREFIX + "a command (or subcommand) name should be followed by object literals with the next subcommands as keys (or empty)."

	objLit0, ok := n.(*ObjectLiteral)
	if !ok {
		return nil, errors.New(ERR)
	}

	for _, p0 := range objLit0.Properties {

		if p0.HasImplicitKey() {
			return nil, errors.New(ERR)
		}

		cmdName := p0.Name()

		objLit1, ok := p0.Value.(*ObjectLiteral)
		if !ok {
			return nil, errors.New(ERR)
		}

		if len(objLit1.Properties) == 0 {
			cmdPerm := CommandPermission{
				CommandName: cmdName,
			}
			perms = append(perms, cmdPerm)
			continue
		}

		for _, p1 := range objLit1.Properties {

			if p1.HasImplicitKey() {
				return nil, errors.New(ERR)
			}

			subcmdName := p1.Name()

			objLit2, ok := p1.Value.(*ObjectLiteral)
			if !ok {
				return nil, errors.New(ERR)
			}

			if len(objLit2.Properties) == 0 {
				subcommandPerm := CommandPermission{
					CommandName:         cmdName,
					SubcommandNameChain: []string{subcmdName},
				}
				perms = append(perms, subcommandPerm)
				continue
			}

			for _, p2 := range objLit2.Properties {

				if p2.HasImplicitKey() {
					return nil, errors.New(ERR)
				}

				deepSubcmdName := p2.Name()

				objLit3, ok := p2.Value.(*ObjectLiteral)
				if !ok {
					return nil, errors.New(ERR)
				}

				if len(objLit3.Properties) == 0 {
					subcommandPerm := CommandPermission{
						CommandName:         cmdName,
						SubcommandNameChain: []string{subcmdName, deepSubcmdName},
					}
					perms = append(perms, subcommandPerm)
					continue
				}

				return nil, errors.New(ERR_PREFIX + "the subcommand chain has a maximum length of 2")
			}
		}
	}

	return perms, nil
}

//this function get permissions limitations & by evaluating a "requirement" object literal
//custom permissions & most limitations are handled by the handleCustomType argument (optional)
func (objLit ObjectLiteral) PermissionsLimitations(
	globalConsts *GlobalConstantDeclarations,
	runningState *State,
	handleCustomType func(kind PermissionKind, name string, value Node) (perms []Permission, handled bool, err error),
) ([]Permission, []Limitation) {

	perms := make([]Permission, 0)
	limitations := make([]Limitation, 0)

	if (globalConsts != nil) && (runningState != nil) {
		log.Panicln("Permissions(): invalid arguments: both arguments cannot be non nil")
	}

	var state *State
	if globalConsts != nil {
		state = NewState(NewContext([]Permission{GlobalVarPermission{ReadPerm, "*"}}, nil, nil))
		globalScope := state.GlobalScope()
		for _, decl := range globalConsts.Declarations {
			globalScope[decl.Left.Name] = MustEval(decl.Right, nil)
		}
	} else {
		state = runningState
	}

	for _, prop := range objLit.Properties {
		name := prop.Name()
		permKind, ok := PermissionKindFromString(name)

		if !ok {
			if name != "limits" {
				log.Panicln("invalid requirements, invalid permission kind:", name)
			}

			limitObjLiteral, isObjLit := prop.Value.(*ObjectLiteral)
			if !isObjLit {
				log.Panicln("invalid requirements, limits should be an object literal:", name)
			}

			//add limits

			for _, limitProp := range limitObjLiteral.Properties {

				switch node := limitProp.Value.(type) {
				case *RateLiteral:
					limitation := Limitation{
						Name: limitProp.Name(),
					}
					rate := MustEval(node, state)

					switch r := rate.(type) {
					case ByteRate:
						limitation.ByteRate = r
					case SimpleRate:
						limitation.SimpleRate = r
					default:
						log.Panicf("not a valid rate type %T\n", r)
					}

					limitations = append(limitations, limitation)
				case *IntLiteral:
					limitation := Limitation{
						Name:  limitProp.Name(),
						Total: int64(node.Value),
					}
					limitations = append(limitations, limitation)
				case *QuantityLiteral:
					limitation := Limitation{
						Name: limitProp.Name(),
					}
					total := UnwrapReflectVal(MustEval(node, state))

					switch d := total.(type) {
					case time.Duration:
						limitation.Total = int64(d)
					default:
						log.Panicf("not a valid total type %T\n", d)
					}
					limitations = append(limitations, limitation)
				default:
					log.Panicln("invalid requirements, limits: only byte rate literals are supported for now.")
				}
			}

			//check & postprocess limits

			for i, l := range limitations {
				switch l.Name {
				case EXECUTION_TOTAL_LIMIT_NAME:
					if l.Total == 0 {
						log.Panicf("invalid requirements, limits: %s should have a total value\n", EXECUTION_TOTAL_LIMIT_NAME)
					}
					l.DecrementFn = func(lastDecrementTime time.Time) int64 {
						v := TOKEN_BUCKET_CAPACITY_SCALE * time.Since(lastDecrementTime)
						return v.Nanoseconds()
					}
				}
				limitations[i] = l
			}

			continue
		}

		var nodes []Node
		switch vn := prop.Value.(type) {
		case *ListLiteral:
			nodes = vn.Elements
		case *ObjectLiteral:
			for _, p := range vn.Properties {
				if p.Key == nil {
					nodes = append(nodes, p.Value)
				} else {
					typeName := p.Name()
					switch typeName {
					case "globals":
						globalReqNodes := make([]Node, 0)

						switch valueNode := p.Value.(type) {
						case *ListLiteral:
							globalReqNodes = append(globalReqNodes, valueNode.Elements...)
						default:
							globalReqNodes = append(globalReqNodes, valueNode)
						}

						for _, gn := range globalReqNodes {
							nameOrAny, ok := gn.(*StringLiteral)
							if !ok { //TODO: + check with regex
								log.Panicln("invalid requirements, 'globals' should be followed by a (or a list of) variable name(s) or a start *")
							}

							perms = append(perms, GlobalVarPermission{
								Kind_: permKind,
								Name:  nameOrAny.Value,
							})
						}
					case "contextless":
						if permKind != UsePerm {
							log.Panic("permission 'contextless' should be in the 'use' section of permissions")
						}
						contextlessDesc, isObjLit := p.Value.(*ObjectLiteral)
						if !isObjLit {
							log.Panicln("invalid requirements, 'contextless' should have an object literal value")
						}

						for _, ctxlessProp := range contextlessDesc.Properties {

							if ctxlessProp.HasImplicitKey() { //a function's name
								identLit, ok := ctxlessProp.Value.(*IdentifierLiteral)
								if !ok {
									log.Panicln("invalid requirements, 'contextless' description: implicity key props should be function names (identifiers)")
								}
								perms = append(perms, ContextlessCallPermission{
									FuncMethodName: identLit.Name,
								})
								continue
							}

							//else: receiver type
							receiverTypeName := ctxlessProp.Name()
							objLit, ok := ctxlessProp.Value.(*ObjectLiteral)
							if !ok {
								log.Panicln("invalid requirements, 'contextless' description: non-implicit-key props should be object literals")
							}

							for _, receiverDescProp := range objLit.Properties {
								terminalDesc, isObjLit := receiverDescProp.Value.(*ObjectLiteral)
								if !isObjLit || receiverDescProp.HasImplicitKey() {
									log.Panicf("invalid requirements, 'contextless' description: description of receiver type '%s': only implicit-key props with an object literal value are allwoed\n", receiverTypeName)
								}
								perms = append(perms, ContextlessCallPermission{
									FuncMethodName:   receiverDescProp.Name(),
									ReceiverTypeName: receiverTypeName,
								})

								_ = terminalDesc //future use
							}
						}
					case "routines":
						switch p.Value.(type) {
						case *ObjectLiteral:
							perms = append(perms, RoutinePermission{permKind})
						default:
							log.Panicln("invalid requirements, 'routines' should be followed by an object literal")
						}
					case "commands":
						if permKind != UsePerm {
							log.Panic("permission 'commands' should be required in the 'use' section of permission")
						}

						newPerms, err := getCommandPermissions(p.Value)
						if err != nil {
							log.Panic(err.Error())
						}
						perms = append(perms, newPerms...)
					default:
						if handleCustomType != nil {
							customPerms, handled, err := handleCustomType(permKind, typeName, p.Value)
							if handled {
								if err != nil {
									log.Panicf("invalid requirements, cannot infer '%s' permission '%s': %s\n", name, typeName, err.Error())
								}
								perms = append(perms, customPerms...)
								break
							}
						}

						log.Panicf("invalid requirements, cannot infer '%s' permission '%s'\n", name, typeName)
					}

				}
			}
		default:
			nodes = []Node{vn}
		}

		for _, n := range nodes {
			if !IsSimpleValueLiteral(n) {
				if _, ok := n.(*GlobalVariable); !ok {
					log.Panicf("invalid requirements, cannot infer permission, node is a(n) %T \n", n)
				}
			}

			value := MustEval(n, state)

			switch v := value.(type) {
			case URL:
				perms = append(perms, HttpPermission{
					Kind_:  permKind,
					Entity: v,
				})
			case URLPattern:
				perms = append(perms, HttpPermission{
					Kind_:  permKind,
					Entity: v,
				})
			case HTTPHost:
				perms = append(perms, HttpPermission{
					Kind_:  permKind,
					Entity: v,
				})
			case HTTPHostPattern:
				perms = append(perms, HttpPermission{
					Kind_:  permKind,
					Entity: v,
				})
			case Path:
				perms = append(perms, FilesystemPermission{
					Kind_:  permKind,
					Entity: v,
				})
				if !v.isAbsolute() {
					log.Panicf("invalid requirements, only absolute paths are accepted: %s\n", v)
				}
			case PathPattern:
				perms = append(perms, FilesystemPermission{
					Kind_:  permKind,
					Entity: v,
				})
				if !v.isAbsolute() {
					log.Panicf("invalid requirements, only absolute path patterns are accepted: %s\n", v)
				}
			default:
				log.Panicf("invalid requirements, cannot infer permission, value is a(n) %T \n", v)
			}
		}

	}
	return perms, limitations
}

type ObjectProperty struct {
	NodeBase
	Key   Node //can be nil (implicit key)
	Value Node
}

func (prop ObjectProperty) HasImplicitKey() bool {
	return prop.Key == nil
}

func (prop ObjectProperty) Name() string {
	switch v := prop.Key.(type) {
	case *IdentifierLiteral:
		return v.Name
	case *StringLiteral:
		return v.Value
	default:
		panic(fmt.Errorf("invalid key type %T", v))
	}
}

type ListLiteral struct {
	NodeBase
	Elements []Node
}

type IdentifierLiteral struct {
	NodeBase
	Name string
}

type PatternIdentifierLiteral struct {
	NodeBase
	Name string
}

type ObjectPatternLiteral struct {
	NodeBase
	Properties []ObjectProperty
}

type ListPatternLiteral struct {
	NodeBase
	Elements []Node
}

type GlobalConstantDeclarations struct {
	NodeBase
	Declarations []*GlobalConstantDeclaration
}

type GlobalConstantDeclaration struct {
	NodeBase
	Left  *IdentifierLiteral
	Right Node
}

type Assignment struct {
	NodeBase
	Left  Node
	Right Node
}

type MultiAssignment struct {
	NodeBase
	Variables []Node
	Right     Node
}

type HostAliasDefinition struct {
	NodeBase
	Left  *AtHostLiteral
	Right Node
}

type Call struct {
	NodeBase
	Callee    Node
	Arguments []Node
	Must      bool
}

type IfStatement struct {
	NodeBase
	Test       Node
	Consequent *Block
	Alternate  *Block //can be nil
}

type ForStatement struct {
	NodeBase
	KeyIndexIdent  *IdentifierLiteral //can be nil
	ValueElemIdent *IdentifierLiteral //can be nil
	Body           *Block
	IteratedValue  Node
}

type Block struct {
	NodeBase
	Statements []Node
}

type ReturnStatement struct {
	NodeBase
	Expr Node
}

type BreakStatement struct {
	NodeBase
	Label *IdentifierLiteral //can be nil
}

type ContinueStatement struct {
	NodeBase
	Label *IdentifierLiteral //can be nil
}

type SwitchStatement struct {
	NodeBase
	Discriminant Node
	Cases        []*Case
}

type Case struct {
	NodeBase
	Value Node
	Block *Block
}

type MatchStatement struct {
	NodeBase
	Discriminant Node
	Cases        []*Case
}

type BinaryOperator int

const (
	Add BinaryOperator = iota
	AddF
	Sub
	SubF
	Mul
	MulF
	Div
	DivF
	Concat
	LessThan
	LessThanF
	LessOrEqual
	LessOrEqualF
	GreaterThan
	GreaterThanF
	GreaterOrEqual
	GreaterOrEqualF
	Equal
	NotEqual
	In
	NotIn
	Keyof
	Dot //unused, present for symmetry
	Range
	ExclEndRange
	And
	Or
	Match
	NotMatch
	Substrof
)

var BINARY_OPERATOR_STRINGS = []string{
	"+", "+.", "-", "-.", "*", "*.", "/", "/.", "++", "<", "<.", "<=", "<=", ">", ">.", ">=", ">=.", "==", "!=",
	"in", "not-in", "keyof", ".", "..", "..<", "and", "or", "match", "not-match", "Substrof",
}

func (operator BinaryOperator) String() string {
	return BINARY_OPERATOR_STRINGS[int(operator)]
}

type BinaryExpression struct {
	NodeBase
	Operator BinaryOperator
	Left     Node
	Right    Node
}

type IntegerRangeLiteral struct {
	NodeBase
	LowerBound *IntLiteral
	UpperBound *IntLiteral
}

type UpperBoundRangeExpression struct {
	NodeBase
	UpperBound Node
}

type RuneRangeExpression struct {
	NodeBase
	Lower *RuneLiteral
	Upper *RuneLiteral
}

type FunctionExpression struct {
	NodeBase
	Parameters   []*FunctionParameter
	Body         *Block
	Requirements *Requirements
}

type FunctionDeclaration struct {
	NodeBase
	Function *FunctionExpression
	Name     *IdentifierLiteral
}

type FunctionParameter struct {
	NodeBase
	Var *IdentifierLiteral
}

type Requirements struct {
	ValuelessTokens []ValuelessToken
	Object          *ObjectLiteral
}

type PermissionDroppingStatement struct {
	NodeBase
	Object *ObjectLiteral
}

type ImportStatement struct {
	NodeBase
	Identifier         *IdentifierLiteral
	URL                *URLLiteral
	ValidationString   *StringLiteral
	ArgumentObject     *ObjectLiteral
	GrantedPermissions *ObjectLiteral
}

type LazyExpression struct {
	NodeBase
	Expression Node
}

type SpawnExpression struct {
	NodeBase
	GroupIdent         *IdentifierLiteral //can be nil
	Globals            Node
	ExprOrVar          Node
	GrantedPermissions *ObjectLiteral //nil if no "allow ...." in the spawn expression
}

type PipelineStatement struct {
	NodeBase
	Stages []*PipelineStage
}

type PipelineExpression struct {
	NodeBase
	Stages []*PipelineStage
}

type PipelineStageKind int

const (
	NormalStage PipelineStageKind = iota
	ParallelStage
)

type PipelineStage struct {
	Kind PipelineStageKind
	Expr Node
}

type PatternDefinition struct {
	NodeBase
	Left  *PatternIdentifierLiteral
	Right Node
}

type PatternKind int

const (
	UnspecifiedPatternKind PatternKind = iota
	StringPattern
	IntegerPattern
	FloatPattern
)

type PatternPiece struct {
	NodeBase
	Kind     PatternKind
	Elements []*PatternPieceElement
}

type OcurrenceCountModifier int

const (
	ExactlyOneOcurrence OcurrenceCountModifier = iota
	AtLeastOneOcurrence
	ZeroOrMoreOcurrence
	OptionalOcurrence
	ExactOcurrence
)

type PatternPieceElement struct {
	NodeBase
	Ocurrence           OcurrenceCountModifier
	ExactOcurrenceCount int
	Expr                Node
}

type PatternUnion struct {
	NodeBase
	Cases []Node
}

func IsSimpleValueLiteral(node Node) bool {
	switch node.(type) {
	case *StringLiteral, *IdentifierLiteral, *IntLiteral, *FloatLiteral, *AbsolutePathLiteral, *AbsolutePathPatternLiteral, *RelativePathLiteral,
		*RelativePathPatternLiteral, *NamedSegmentPathPatternLiteral, *RegularExpressionLiteral, *BooleanLiteral, *NilLiteral, *HTTPHostLiteral, *HTTPHostPatternLiteral, *URLLiteral, *URLPatternLiteral:
		return true
	default:
		return false
	}
}

func Is(node Node, typ interface{}) bool {
	return reflect.TypeOf(typ) == reflect.TypeOf(node)
}

type NodeCategory int

const (
	UnspecifiedCategory NodeCategory = iota
	URLlike
	Pathlike
	IdentLike
	KnownType
)

// the following types are considered as Gopherscript values

//int, float64, string, bool, reflect.Value
type Object map[string]interface{}
type List []interface{}
type KeyList []string
type Func Node
type ExternalValue struct {
	state *State
	value interface{}
}
type Option struct {
	Name  string
	Value interface{}
}

//special string types
type JSONstring string
type Path string
type PathPattern string
type URL string
type HTTPHost string
type HTTPHostPattern string
type URLPattern string
type Identifier string

// ---------------------------

func (obj Object) GetOrDefault(key string, defaultVal interface{}) interface{} {
	v, ok := obj[key]
	if !ok {
		return defaultVal
	}
	return v
}

type indexedEntryIterator struct {
	i      int
	len    int
	object Object
}

func (it *indexedEntryIterator) HasNext(*Context) bool {
	return it.i < it.len
}

func (it *indexedEntryIterator) GetNext(*Context) interface{} {
	res := it.object[strconv.Itoa(it.i)]
	it.i++
	return res
}

func (obj Object) Indexed() Iterator {

	length, hasLen := obj[IMPLICIT_KEY_LEN_KEY]
	if !hasLen {
		length = 0
	}

	//TODO: add more checks

	return &indexedEntryIterator{
		i:      0,
		object: obj,
		len:    length.(int),
	}
}

func (list List) ContainsSimple(v interface{}) bool {
	if !IsSimpleGopherVal(v) {
		panic("only simple values are expected")
	}

	for _, e := range list {
		if v == e {
			return true
		}
	}
	return false
}

func IsIndexKey(key string) bool {
	_, err := strconv.ParseUint(key, 10, 32)
	return err == nil
}

func (pth Path) IsDirPath() bool {
	return pth[len(pth)-1] == '/'
}

func (pth Path) isAbsolute() bool {
	return pth[0] == '/'
}

func (pth Path) ToAbs() Path {
	if pth.isAbsolute() {
		return pth
	}
	s, err := filepath.Abs(string(pth))
	if err != nil {
		panic(fmt.Errorf("path resolution: %s", err))
	}
	if pth.IsDirPath() && s[len(s)-1] != '/' {
		s += "/"
	}
	return Path(s)
}

func (patt PathPattern) isAbsolute() bool {
	return patt[0] == '/'
}

func (patt PathPattern) IsPrefixPattern() bool {
	return strings.HasSuffix(string(patt), "/...")
}

func (patt PathPattern) Prefix() string {
	if patt.IsPrefixPattern() {
		return string(patt[0 : len(patt)-len("/...")])
	}
	return string(patt)
}

func (patt PathPattern) ToAbs() PathPattern {
	if patt.isAbsolute() {
		return patt
	}
	s, err := filepath.Abs(string(patt))
	if err != nil {
		panic(fmt.Errorf("path pattern resolution: %s", err))
	}
	return PathPattern(s)
}

func (patt URLPattern) Prefix() string {
	return string(patt[0 : len(patt)-len("/...")])
}

//type : reflect.Value

type Matcher interface {
	Test(interface{}) bool
}

type GroupMatcher interface {
	Matcher
	MatchGroups(interface{}) (ok bool, groups map[string]interface{})
}

//todo: improve name
type GenerativePattern interface {
	Random() interface{}
}

func (patt PathPattern) Test(v interface{}) bool {
	switch other := v.(type) {
	case Path:
		if patt.IsPrefixPattern() {
			return strings.HasPrefix(string(other), patt.Prefix())
		}
		ok, err := path.Match(string(patt), string(other))
		return err == nil && ok
	case PathPattern:
		if patt.IsPrefixPattern() {
			return strings.HasPrefix(string(other), patt.Prefix())
		}
		return patt == other
	default:
		return false
	}
}

func (patt HTTPHostPattern) Test(v interface{}) bool {
	var url_ string

	switch other := v.(type) {
	case HTTPHostPattern:
		return patt == other
	case HTTPHost:
		url_ = string(other)
	case URL:
		url_ = string(other)
	}
	otherURL, err := url.Parse(url_)
	if err != nil {
		return false
	}

	regex := strings.ReplaceAll(string(patt), ".", "\\.")
	if strings.HasPrefix(regex, "https") {
		regex = strings.ReplaceAll(regex, ":443", "")
	} else {
		regex = strings.ReplaceAll(regex, ":80", "")
	}
	regex = strings.ReplaceAll(regex, "/", "\\/")
	if strings.Count(regex, "*") == 1 {
		regex = "^" + strings.ReplaceAll(regex, "*", "[-a-zA-Z0-9.]+") + "$"
	} else {
		regex = "^" + strings.ReplaceAll(regex, "*", "[-a-zA-Z0-9]+") + "$"
	}

	httpsHost := otherURL.Scheme + "://" + otherURL.Host
	if otherURL.Scheme == "https" {
		httpsHost = strings.ReplaceAll(httpsHost, ":443", "")
	} else {
		httpsHost = strings.ReplaceAll(httpsHost, ":80", "")
	}

	ok, err := regexp.Match(regex, []byte(httpsHost))
	return err == nil && ok
}

func (patt URLPattern) Test(v interface{}) bool {
	switch other := v.(type) {
	case HTTPHostPattern, HTTPHost:
		return false
	case URL:
		return strings.HasPrefix(string(other), patt.Prefix())
	default:
		return false
	}
}

type ExactSimpleValueMatcher struct{ value interface{} }

func (matcher ExactSimpleValueMatcher) Test(v interface{}) bool {
	return matcher.value == v
}

func (matcher ExactSimpleValueMatcher) Regex() string {
	s, isString := matcher.value.(string)
	if !isString {
		panic(errors.New("cannot get regex for a ExactSimpleValueMatcher that has a non-string value"))
	}
	return regexp.QuoteMeta(string(s))
}

func (matcher ExactSimpleValueMatcher) Random() interface{} {
	return matcher.value
}

type RegexMatcher struct{ regexp *regexp.Regexp }

func (matcher RegexMatcher) Test(v interface{}) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	return matcher.regexp.MatchString(str)
}

func (matcher RegexMatcher) Regex() string {
	return matcher.regexp.String()
}

func samePointer(a, b interface{}) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

//CallFunc calls calleeNode, whatever its kind (Gopherscript function or Go function).
//If must is true and the second result of a Go function is a non-nil error, CallFunc will panic.
func CallFunc(calleeNode Node, state *State, arguments interface{}, must bool) (interface{}, error) {
	state.ctx.Take(EXECUTION_TOTAL_LIMIT_NAME, 1)

	stackHeight := 1 + len(state.ScopeStack)

	if !state.ctx.stackPermission.includes(StackPermission{maxHeight: stackHeight}) {
		return nil, errors.New("cannot call: stack height limit reached")
	}

	var callee interface{}
	var optReceiverType *reflect.Type
	var methodName string
	var err error

	//we first get the callee
	switch c := calleeNode.(type) {
	case *IdentifierLiteral:
		err := state.ctx.CheckHasPermission(GlobalVarPermission{Kind_: UsePerm, Name: c.Name})
		if err != nil {
			return nil, err
		}
		methodName = c.Name
		callee = state.GlobalScope()[c.Name]
	case *IdentifierMemberExpression:
		name := c.Left.Name
		err := state.ctx.CheckHasPermission(GlobalVarPermission{Kind_: UsePerm, Name: name})
		if err != nil {
			return nil, err
		}

		v, ok := state.GlobalScope()[name]

		if !ok {
			return nil, errors.New("global variable " + name + " is not declared")
		}

		for _, idents := range c.PropertyNames {
			methodName = idents.Name
			v, optReceiverType, err = Memb(v, idents.Name)
			if err != nil {
				return nil, err
			}
		}
		callee = v
	case *Variable:
		callee, err = Eval(calleeNode, state)
		if err != nil {
			return nil, err
		}
	case *MemberExpression:
		left, err := Eval(c.Left, state)
		if err != nil {
			return nil, err
		}

		methodName = c.PropertyName.Name
		callee, optReceiverType, err = Memb(left, c.PropertyName.Name)
		if err != nil {
			return nil, err
		}
	case *FunctionDeclaration, *FunctionExpression:
		callee = c
	default:
		return nil, errors.New("only identifier callee supported for now")
	}

	if callee == nil {
		return nil, fmt.Errorf("cannot call nil %#v", calleeNode)
	}

	var extState *State
	ext, isExt := callee.(ExternalValue)
	if isExt {
		extState = ext.state
		callee = ext.value
	}

	//EVALUATION OF ARGUMENTS

	args := List{}

	if l, ok := arguments.(List); ok {
		args = l
	} else {
		for _, argn := range arguments.([]Node) {
			arg, err := Eval(argn, state)
			if err != nil {
				return nil, err
			}
			if isExt {
				arg = ExtValOf(arg, extState)
			}
			args = append(args, arg)
		}
	}

	//EXECUTION

	var fn *FunctionExpression
	switch f := callee.(type) {
	case *FunctionExpression:
		fn = f
		if must {
			log.Panicln("'must' function calls are only supported for Go functions")
		}
	case *FunctionDeclaration:
		fn = f.Function
		if must {
			log.Panicln("'must' function calls are only supported for Go functions")
		}
	default:
		//GO FUNCTION

		fnVal := f.(reflect.Value)
		fnValType := fnVal.Type()

		if fnVal.Kind() != reflect.Func {
			log.Panicf("cannot call %#v\n", f)
		}

		isfirstArgCtx := false
		var ctx *Context = state.ctx
		if isExt {
			ctx = extState.ctx
		}

		if fnValType.NumIn() == 0 || !CTX_PTR_TYPE.AssignableTo(fnValType.In(0)) {
			var funcName string

			var receiverTypeName string
			if optReceiverType == nil {
				fullNameParts := strings.Split(runtime.FuncForPC(fnVal.Pointer()).Name(), ".")
				funcName = strings.TrimSuffix(fullNameParts[len(fullNameParts)-1], "-fm")
			} else {
				receiverTypeName = (*optReceiverType).Name()
				funcName = methodName
			}

			if err := ctx.CheckHasPermission(ContextlessCallPermission{
				ReceiverTypeName: receiverTypeName,
				FuncMethodName:   funcName,
			}); err != nil {

				if optReceiverType == nil {
					return nil, fmt.Errorf("cannot call contextless function with name '%s': %s", funcName, err.Error())
				}
				return nil, fmt.Errorf("cannot call contextless method: receiver '%s', name '%s': %s", receiverTypeName, funcName, err.Error())
			}
		} else {
			isfirstArgCtx = true
		}

		if isfirstArgCtx {
			args = append(List{ctx}, args...)
		}

		if len(args) != fnValType.NumIn() && (!fnValType.IsVariadic() || len(args) < fnValType.NumIn()-1) {
			return nil, fmt.Errorf("invalid number of arguments : %v, %v was expected", len(args), fnValType.NumIn())
		}

		argValues := make([]reflect.Value, len(args))

		for i, arg := range args {
			if extVal, ok := arg.(ExternalValue); ok {
				arg = extVal.value
			}
			argValue := ToReflectVal(arg)

			if i < fnValType.NumIn() {
				paramType := fnValType.In(i)

				if !argValue.Type().AssignableTo(paramType) {

				conversion:
					switch paramType.Kind() {
					case reflect.Struct:
						//attemp to create a struct
						obj, ok := arg.(Object)
						if !ok {
							break conversion
						}

						argumentValue := reflect.New(paramType).Elem()

						for j := 0; j < paramType.NumField(); j++ {
							field := paramType.Field(j)

							if !field.IsExported() {
								continue
							}

							v, propPresent := obj[field.Name]
							if !propPresent {
								break conversion
							}

							propValue := ToReflectVal(v)
							if !propValue.Type().AssignableTo(field.Type) {
								break conversion
							}

							argumentValue.Field(j).Set(propValue)
						}

						argValue = argumentValue
					}
				}
			}

			argValues[i] = argValue
		}

		resultValues := fnVal.Call(argValues)

		//TODO: do that even for single result functions ?
		if must && fnValType.NumOut() >= 2 &&
			fnValType.Out(fnValType.NumOut()-1).Implements(ERROR_INTERFACE_TYPE) {
			lastElem := resultValues[len(resultValues)-1]

			if lastElem.IsNil() {
				resultValues = resultValues[:len(resultValues)-1]
			} else {
				panic(lastElem.Interface().(error))
			}
		}

		switch len(resultValues) {
		case 1:
			if isExt {
				return ExtValOf(resultValues[0], extState), nil
			}
			return ValOf(resultValues[0]), nil
		}
		results := make(List, 0, len(resultValues))

		if isExt {
			for _, resultValue := range resultValues {
				results = append(results, ExtValOf(resultValue, extState))
			}
		} else {
			for _, resultValue := range resultValues {
				results = append(results, ValOf(resultValue))
			}
		}

		return results, nil
	}

	//GOPHERSCRIPT FUNCTION

	if len(args) != len(fn.Parameters) {
		return nil, fmt.Errorf("invalid number of arguments : %v, %v was expected", len(args), len(fn.Parameters))
	}

	state.PushScope()
	defer state.PopScope()

	for i, p := range fn.Parameters {
		name := p.Var.Name
		state.CurrentScope()[name] = args[i]
	}

	_, err = Eval(fn.Body, state)
	if err != nil {
		return nil, err
	}

	retValuePtr := state.ReturnValue
	if retValuePtr == nil {
		return nil, nil
	}

	defer func() {
		state.ReturnValue = nil
	}()

	ret := *state.ReturnValue
	if isExt {
		ret = ExtValOf(ret, extState)
	}
	return ret, nil

}

type Routine struct {
	node  Node
	state *State

	resultChan chan (interface{})
}

func (routine *Routine) WaitResult(ctx *Context) (interface{}, error) {
	resOrErr := <-routine.resultChan
	if err, ok := resOrErr.(error); ok {
		return nil, err
	}

	return ExtValOf(resOrErr, routine.state), nil
}

type RoutineGroup struct {
	routines []*Routine
}

func (group *RoutineGroup) add(newRt *Routine) {
	for _, rt := range group.routines {
		if rt == newRt {
			panic(errors.New("attempt to add a routine to a group more than once"))
		}
	}
	group.routines = append(group.routines, newRt)
}

func (group *RoutineGroup) WaitAllResults(ctx *Context) (interface{}, error) {
	results := List{}

	for _, rt := range group.routines {
		rtRes, rtErr := rt.WaitResult(ctx)
		if rtErr != nil {
			return nil, rtErr
		}
		results = append(results, rtRes)
	}

	return results, nil
}

func spawnRoutine(state *State, globals map[string]interface{}, moduleOrExpr Node, routineCtx *Context) (*Routine, error) {
	perm := RoutinePermission{Kind_: CreatePerm}

	if err := state.ctx.CheckHasPermission(perm); err != nil {
		return nil, fmt.Errorf("cannot spawn routine: %s", err.Error())
	}

	if err := Check(moduleOrExpr); err != nil {
		return nil, fmt.Errorf("cannot spawn routine: expression: module/expr checking failed: %s", err.Error())
	}

	if routineCtx == nil {
		routineCtx = NewContext([]Permission{
			GlobalVarPermission{ReadPerm, "*"},
			GlobalVarPermission{UsePerm, "*"},
		}, nil, nil)
		routineCtx.limiters = state.ctx.limiters
	}

	modState := NewState(routineCtx, globals)
	resChan := make(chan (interface{}))

	go func(modState *State, moduleOrExpr Node, resultChan chan (interface{})) {
		res, err := Eval(moduleOrExpr, modState)
		if err != nil {
			log.Printf("a routine failed: %s", err.Error())
			resultChan <- err
			return
		}
		resultChan <- res

	}(modState, moduleOrExpr, resChan)

	return &Routine{
		node:       moduleOrExpr,
		state:      modState,
		resultChan: resChan,
	}, nil
}

func downloadAndParseModule(importURL URL, validation string) (*Module, error) {
	client := http.Client{
		Timeout: 10 * time.Second,
	}

	var modString string
	var ok bool

	if modString, ok = MODULE_CACHE[validation]; !ok {
		req, err := http.NewRequest("GET", string(importURL), nil)
		req.Header.Add("Accept", GOPHERSCRIPT_MIMETYPE)

		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if resp != nil { //on redirection failure resp will be non nil
			defer resp.Body.Close()
		}

		if err != nil {
			return nil, err
		}

		//TODO: sanitize .Status, Content-Type, etc before writing them to the terminal
		b, bodyErr := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("failed to get %s: status %d: %s", importURL, resp.StatusCode, resp.Status)
		}

		ctype := resp.Header.Get("Content-Type")
		if ctype != GOPHERSCRIPT_MIMETYPE {
			return nil, fmt.Errorf("failed to get %s: content-type is '%s'", importURL, ctype)
		}

		if bodyErr != nil {
			return nil, fmt.Errorf("failed to get %s: failed to read body: %s", importURL, err.Error())
		}

		array := sha256.Sum256(b)
		hash := array[:]

		validationBytes := []byte(validation)
		if !bytes.Equal(hash, validationBytes) {
			if bodyErr != nil {
				return nil, fmt.Errorf("failed to get %s: validation failed", importURL)
			}
		}
		modString = string(b)
		MODULE_CACHE[validation] = modString

		//TODO: limit cache size
	}

	mod, err := ParseAndCheckModule(modString, string(importURL))
	if err != nil {
		return nil, err
	}

	return mod, nil
}

func ParseAndCheckModule(s string, fpath string) (*Module, error) {
	mod, err := ParseModule(s, fpath)
	if err != nil {
		return nil, err
	}
	if err := Check(mod); err != nil {
		return nil, err
	}
	return mod, nil
}

type ParsingError struct {
	Message string
	Index   int

	NodeStartIndex int //< 0 if not specified
	NodeCategory   NodeCategory
	NodeType       Node //not nil if .NodeCategory is KnownType
}

func (err ParsingError) Error() string {
	return err.Message
}

func MustParseModule(str string) (result *Module) {
	n, err := ParseModule(str, "<chunk>")
	if err != nil {
		panic(err)
	}
	return n
}

//parses a file module, resultErr is either a non-sntax error or an aggregation of syntax errors.
//result and resultErr can be both non-nil at the same time because syntax errors are also stored in each node.
func ParseModule(str string, fpath string) (result *Module, resultErr error) {
	s := []rune(str)

	defer func() {
		v := recover()
		if err, ok := v.(error); ok {
			resultErr = err
		}

		if resultErr != nil {
			resultErr = fmt.Errorf("%s: %s", resultErr.Error(), debug.Stack())
		}

		if result != nil {
			Walk(result, func(node, parent, scopeNode Node, ancestorChain []Node) (error, TraversalAction) {
				if reflect.ValueOf(node).IsNil() {
					return nil, Continue
				}

				parsingErr := node.Base().Err
				if parsingErr == nil {
					return nil, Continue
				}

				if resultErr == nil {
					resultErr = errors.New("")
				}

				//add location in error message
				line := 1
				col := 1
				i := 0

				for i < parsingErr.Index {
					if s[i] == '\n' {
						line++
						col = 1
					} else {
						col++
					}

					i++
				}

				resultErr = fmt.Errorf("%s\n%s:%d:%d: %s", resultErr.Error(), fpath, line, col, parsingErr.Message)
				return nil, Continue
			})
		}

	}()

	mod := &Module{
		NodeBase: NodeBase{
			Span: NodeSpan{Start: 0, End: len(s)},
		},
		Statements: nil,
	}

	i := 0

	//start of closures

	eatComment := func() bool {
		if i < len(s)-1 && (s[i+1] == ' ' || s[i+1] == '\t') {
			i += 2
			for i < len(s) && s[i] != '\n' {
				i++
			}
			return true
		} else {
			return false
		}
	}

	eatSpace := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}

	eatSpaceAndComments := func() {
		for i < len(s) {
			switch s[i] {
			case ' ', '\t':
				i++
			case '#':
				if !eatComment() {
					return
				}
			default:
				return
			}
		}
	}

	eatSpaceAndNewLineAndComment := func() {
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\n':
				i++
			case '#':
				if !eatComment() {
					return
				}
			default:
				return
			}
		}
	}

	eatSpaceAndNewLineAndCommaAndComment := func() {
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\n', ',':
				i++
			case '#':
				if !eatComment() {
					return
				}
			default:
				return
			}
		}
	}

	eatSpaceNewLineSemiColonComment := func() {
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\n', ';':
				i++
			case '#':
				if !eatComment() {
					return
				}
			default:
				return
			}
		}
	}

	eatSpaceNewlineComma := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == ',') {
			i++
		}
	}

	eatSpaceComma := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == ',') {
			i++
		}
	}

	// eatNewlineAndComma := func() {
	// 	for i < len(s) && (s[i] == '\n' || s[i] == ',') {
	// 		i++
	// 	}
	// }

	var parseBlock func() *Block
	var parseExpression func() (Node, bool)
	var parseStatement func() Statement
	var parseGlobalConstantDeclarations func() *GlobalConstantDeclarations
	var parseRequirements func() *Requirements
	var parseFunction func(int) Node

	parseBlock = func() *Block {
		openingBraceIndex := i
		i++
		var parsingErr *ParsingError

		var stmts []Node

		for i < len(s) && s[i] != '}' {
			eatSpaceNewLineSemiColonComment()

			if i < len(s) && s[i] == '}' {
				break
			}

			stmts = append(stmts, parseStatement())
			eatSpaceNewLineSemiColonComment()
		}

		closingBraceIndex := i

		if i >= len(s) {
			parsingErr = &ParsingError{
				"unterminated block, missing closing brace '}",
				i,
				openingBraceIndex,
				KnownType,
				(*Block)(nil),
			}

		} else {
			i++
		}

		end := i
		mod.Statements = stmts

		return &Block{
			NodeBase: NodeBase{
				Span: NodeSpan{openingBraceIndex, end},
				Err:  parsingErr,
				ValuelessTokens: []ValuelessToken{
					{OPENING_CURLY_BRACKET, NodeSpan{openingBraceIndex, openingBraceIndex + 1}},
					{CLOSING_CURLY_BRACKET, NodeSpan{closingBraceIndex, closingBraceIndex + 1}},
				},
			},
			Statements: stmts,
		}
	}

	countPrevBackslashes := func() int {
		index := i - 1
		count := 0
		for ; index >= 0 && index != '"'; index-- {
			if s[index] == '\\' {
				count += 1
			} else {
				break
			}
		}

		return count
	}

	parsePathExpressionSlices := func(start int, exclEnd int) []Node {
		slices := make([]Node, 0)
		index := start
		sliceStart := start
		inInterpolation := false

		for index < exclEnd {

			if inInterpolation {
				if s[index] == '$' { //end if interpolation
					interpolation := string(s[sliceStart:index])

					res, err := ParseModule(interpolation, "")

					if err != nil {
						slices = append(slices, &UnknownNode{
							NodeBase: NodeBase{
								NodeSpan{sliceStart, exclEnd},
								&ParsingError{
									"invalid path interpolation",
									i,
									-1,
									UnspecifiedCategory,
									nil,
								},
								nil,
							},
						})
					} else {
						shiftNodeSpans(res, sliceStart)
						slices = append(slices, res.Statements[0])
					}

					inInterpolation = false
					sliceStart = index + 1
				} else if !isInterpolationAllowedChar(s[index]) {
					slices = append(slices, &PathSlice{
						NodeBase: NodeBase{
							NodeSpan{sliceStart, exclEnd},
							&ParsingError{
								"a path interpolation should contain an identifier without spaces, example: $name$ ",
								i,
								-1,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
						Value: string(s[sliceStart:exclEnd]),
					})

					return slices
				}

			} else if s[index] == '$' {
				slice := string(s[sliceStart:index]) //previous cannot be an interpolation

				slices = append(slices, &PathSlice{
					NodeBase: NodeBase{
						NodeSpan{sliceStart, index},
						nil,
						nil,
					},
					Value: slice,
				})

				sliceStart = index
				inInterpolation = true
			}
			index++
		}

		if inInterpolation {
			slices = append(slices, &InvalidPathSlice{
				NodeBase: NodeBase{
					NodeSpan{sliceStart, index},
					&ParsingError{
						"unterminated path interpolation",
						index,
						sliceStart,
						Pathlike,
						(*InvalidPathSlice)(nil),
					},
					nil,
				},
			})
		} else if sliceStart != index {
			slices = append(slices, &PathSlice{
				NodeBase: NodeBase{
					NodeSpan{sliceStart, index},
					nil,
					nil,
				},
				Value: string(s[sliceStart:index]),
			})
		}
		return slices
	}

	parseQueryExpressionSlices := func(start int, exclEnd int) []Node {
		slices := make([]Node, 0)
		index := start
		sliceStart := start
		inInterpolation := false

		for index < exclEnd {

			if inInterpolation {
				if s[index] == '$' {
					name := string(s[sliceStart+1 : index])

					slices = append(slices, &Variable{
						NodeBase: NodeBase{
							NodeSpan{sliceStart, index + 1},
							nil,
							nil,
						},
						Name: name,
					})
					inInterpolation = false
					sliceStart = index + 1
				} else if !isIdentChar(s[index]) {

					slices = append(slices, &URLQueryParameterSlice{
						NodeBase: NodeBase{
							NodeSpan{sliceStart, exclEnd},
							&ParsingError{
								"a query parameter interpolation should contain an identifier without spaces, example: $name$ ",
								i,
								-1,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
						Value: string(s[sliceStart:exclEnd]),
					})

					return slices
				}

			} else if s[index] == '$' {
				slice := string(s[sliceStart:index]) //previous cannot be an interpolation

				slices = append(slices, &URLQueryParameterSlice{
					NodeBase: NodeBase{
						NodeSpan{sliceStart, index},
						nil,
						nil,
					},
					Value: slice,
				})

				sliceStart = index
				inInterpolation = true
			}
			index++
		}

		if inInterpolation {
			panic(ParsingError{
				"unterminated path interpolation",
				i,
				-1,
				UnspecifiedCategory,
				nil,
			})
		}

		if sliceStart != index {
			slices = append(slices, &PathSlice{
				NodeBase: NodeBase{
					NodeSpan{sliceStart, index},
					nil,
					nil,
				},
				Value: string(s[sliceStart:index]),
			})
		}
		return slices
	}

	parsePathLikeExpression := func(isPercentPrefixed bool) Node {
		start := i
		isAbsolute := s[i] == '/'
		i++
		//limit to ascii ? limit to ascii alphanum & some chars ?
		for i < len(s) && !isSpace(string(s[i])) && !isDelim(s[i]) {
			i++
		}

		value := string(s[start:i])
		base := NodeBase{
			Span: NodeSpan{start, i},
		}

		for _, r := range value {

			//pattern
			if isPercentPrefixed || ((r == '[' || r == '*' || r == '?') && countPrevBackslashes()%2 == 0) {

				if strings.HasSuffix(value, "/...") {
					panic(ParsingError{
						"prefix path patterns cannot contain globbing patterns '" + value + "'",
						i,
						start,
						Pathlike,
						nil,
					})
				}

				if isPercentPrefixed {
					base.Span.Start = base.Span.Start - 1
				}

				if strings.Contains(value, "$") {

					if !isPercentPrefixed {
						base.Err = &ParsingError{
							"a path pattern with no leading '%' cannot be interpolated '" + value + "'",
							i,
							start,
							Pathlike,
							nil,
						}
						return &NamedSegmentPathPatternLiteral{
							NodeBase: base,
							Slices:   nil,
						}
					}

					if strings.Contains(value, "$$") {
						base.Err = &ParsingError{
							"a complex path pattern literal cannot contain interpolations next to each others",
							i,
							start,
							Pathlike,
							nil,
						}
						return &NamedSegmentPathPatternLiteral{
							NodeBase: base,
							Slices:   nil,
						}
					}

					slices := parsePathExpressionSlices(start, i)

					for j := 0; j < len(slices); j++ {
						_, isVar := slices[j].(*Variable)
						if isVar {
							prev := slices[j-1].(*PathSlice).Value
							if prev[len(prev)-1] != '/' {

								base.Err = &ParsingError{
									"invalid path pattern literal with named segments",
									i,
									start,
									Pathlike,
									nil,
								}

								return &NamedSegmentPathPatternLiteral{
									NodeBase: base,
									Slices:   slices,
								}
							}
							if j < len(slices)-1 {
								next := slices[j+1].(*PathSlice).Value
								if next[0] != '/' {
									base.Err = &ParsingError{
										"invalid path pattern literal with named segments",
										i,
										start,
										Pathlike,
										nil,
									}

									return &NamedSegmentPathPatternLiteral{
										NodeBase: base,
										Slices:   slices,
									}
								}
							}
						}
					}

					return &NamedSegmentPathPatternLiteral{
						NodeBase: base,
						Slices:   slices,
					}
				}

				if isAbsolute {
					return &AbsolutePathPatternLiteral{
						NodeBase: base,
						Value:    value,
					}
				}
				return &RelativePathPatternLiteral{
					NodeBase: base,
					Value:    value,
				}
			}
		}

		if strings.Contains(value, "$") {
			var parsingErr *ParsingError

			if strings.Contains(value, "$$") {
				parsingErr = &ParsingError{
					"a path expression cannot contain interpolations next to each others",
					i,
					start,
					Pathlike,
					nil,
				}
			}

			slices := parsePathExpressionSlices(start, i)

			base.Err = parsingErr

			if isAbsolute {
				return &AbsolutePathExpression{
					NodeBase: base,
					Slices:   slices,
				}
			}
			return &RelativePathExpression{
				NodeBase: base,
				Slices:   slices,
			}
		}

		if strings.Contains(value, "/...") {
			var parsingErr *ParsingError

			if !strings.HasSuffix(value, "/...") {
				parsingErr = &ParsingError{
					"'/...' can only be present at the end of a path pattern  '" + value + "'",
					i,
					start,
					Pathlike,
					nil,
				}
				base.Err = parsingErr
			}

			if isAbsolute {
				return &AbsolutePathPatternLiteral{
					NodeBase: base,
					Value:    value,
				}
			}
			return &RelativePathPatternLiteral{
				NodeBase: base,
				Value:    value,
			}
		}

		if isAbsolute {
			return &AbsolutePathLiteral{
				NodeBase: base,
				Value:    value,
			}
		}
		return &RelativePathLiteral{
			NodeBase: base,
			Value:    value,
		}
	}

	parseURLLike := func(start int) Node {
		i += 3
		for i < len(s) && !isSpace(string(s[i])) && (!isDelim(s[i]) || s[i] == ':') {
			i++
		}

		_url := string(s[start:i])
		isPrefixPattern := strings.HasSuffix(_url, "/...")

		//TODO: think about escaping in URLs with '\': specs, server implementations

		span := NodeSpan{start, i}

		if strings.Contains(_url, "..") && (!isPrefixPattern || strings.Count(_url, "..") != 1) {
			return &InvalidURLPattern{
				Value: _url,
				NodeBase: NodeBase{
					Span: span,
					Err: &ParsingError{
						"URL-like patterns cannot contain more than two subsequents dots except /... at the end for URL patterns",
						i,
						start,
						URLlike,
						nil,
					},
				},
			}
		}

		if !HTTP_URL_REGEX.MatchString(_url) {

			switch {
			case LOOSE_HTTP_HOST_PATTERN_REGEX.MatchString(_url):
				pattern := _url[strings.Index(_url, "://")+3:]
				pattern = strings.Split(pattern, ":")[0]
				parts := strings.Split(pattern, ".")

				var parsingErr *ParsingError

				if len(parts) == 1 {
					if parts[0] != "*" {
						parsingErr = &ParsingError{
							"invalid HTTP host pattern '" + _url,
							i,
							start,
							URLlike,
							(*HTTPHostPatternLiteral)(nil),
						}
					}
				} else {
					replaced := strings.ReplaceAll(_url, "*", "com")
					if _, err := url.Parse(replaced); err != nil {

						parsingErr = &ParsingError{
							"invalid HTTP host pattern '" + _url + "' : " + err.Error(),
							i,
							start,
							URLlike,
							(*HTTPHostPatternLiteral)(nil),
						}
					}
				}

				return &HTTPHostPatternLiteral{
					NodeBase: NodeBase{
						Span: span,
						Err:  parsingErr,
					},
					Value: _url,
				}
			case LOOSE_URL_EXPR_PATTERN_REGEX.MatchString(_url):
				var parsingErr *ParsingError

				if strings.Contains(_url, "$$") {
					parsingErr = &ParsingError{
						"an URL expression cannot contain interpolations next to each others",
						i,
						start,
						URLlike,
						nil,
					}
				}

				if isPrefixPattern {
					parsingErr = &ParsingError{
						"an URL expression cannot ends with /...",
						i,
						start,
						URLlike,
						(*URLExpression)(nil),
					}
				}

				pathStart := start

				if strings.Contains(_url, "://") {
					pathStart += strings.Index(_url, "://") + 3
				}

				for s[pathStart] != '/' {
					pathStart++
				}

				pathExclEnd := i
				queryParams := make([]Node, 0)

				if strings.Contains(_url, "?") {
					pathExclEnd = start + strings.Index(_url, "?")

					_, err := url.ParseQuery(string(s[pathExclEnd+1 : start+len(_url)]))
					if err != nil {
						parsingErr = &ParsingError{
							"invalid query",
							i,
							start,
							KnownType,
							(*URLExpression)(nil),
						}
					}

					j := pathExclEnd + 1
					queryEnd := start + len(_url)

					for j < queryEnd {
						keyStart := j
						for j < queryEnd && s[j] != '=' {
							j++
						}
						if j > queryEnd {
							parsingErr = &ParsingError{
								"invalid query: missing '=' after key " + string(s[keyStart:j]),
								i,
								start,
								KnownType,
								(*URLExpression)(nil),
							}
						}

						key := string(s[keyStart:j])
						j++

						//check key

						if strings.Contains(key, "$") {
							parsingErr = &ParsingError{
								"invalid query: keys cannot contain '$': key " + string(s[keyStart:j]),
								i,
								start,
								URLlike,
								(*URLExpression)(nil),
							}
						}

						//value

						valueStart := j
						slices := make([]Node, 0)

						if j < queryEnd && s[j] != '&' {

							for j < queryEnd && s[j] != '&' {
								j++
							}
							slices = parseQueryExpressionSlices(valueStart, j)
						}

						queryParams = append(queryParams, &URLQueryParameter{
							NodeBase: NodeBase{
								NodeSpan{keyStart, j},
								nil,
								nil,
							},
							Name:  key,
							Value: slices,
						})

						if j < queryEnd && s[j] == '&' {
							j++
						}
					}

				}

				slices := parsePathExpressionSlices(pathStart, pathExclEnd)

				var hostPart Node
				hostPartString := string(s[span.Start:pathStart])
				hostPartBase := NodeBase{
					NodeSpan{span.Start, pathStart},
					nil,
					nil,
				}

				if strings.Contains(hostPartString, "://") {
					hostPart = &HTTPHostLiteral{
						NodeBase: hostPartBase,
						Value:    hostPartString,
					}
				} else {
					hostPart = &AtHostLiteral{
						NodeBase: hostPartBase,
						Value:    hostPartString,
					}
				}

				return &URLExpression{
					NodeBase: NodeBase{span, parsingErr, nil},
					Raw:      _url,
					HostPart: hostPart,
					Path: &AbsolutePathExpression{
						NodeBase: NodeBase{
							NodeSpan{pathStart, pathExclEnd},
							nil,
							nil,
						},
						Slices: slices,
					},
					QueryParams: queryParams,
				}
			}
		}

		//remove this check ?
		if !HTTP_URL_REGEX.MatchString(_url) && _url != "https://localhost" {

			return &InvalidURL{
				NodeBase: NodeBase{
					Span: span,
					Err: &ParsingError{
						"invalid URL '" + _url + "'",
						i,
						start,
						URLlike,
						nil,
					},
				},
				Value: _url,
			}
		}

		parsed, err := url.Parse(_url)
		if err != nil {
			return &InvalidURL{
				NodeBase: NodeBase{
					Span: span,
					Err: &ParsingError{
						"invalid URL '" + _url + "'",
						i,
						start,
						URLlike,
						nil,
					},
				},
				Value: _url,
			}
		}

		if isPrefixPattern {
			var parsingErr *ParsingError
			if strings.Contains(_url, "?") {
				parsingErr = &ParsingError{
					"URL patt&ern literals with a query part are not supported yet'" + _url,
					i,
					start,
					URLlike,
					nil,
				}
			}
			return &URLPatternLiteral{
				NodeBase: NodeBase{
					Span: span,
					Err:  parsingErr,
				},
				Value: _url,
			}
		}

		if strings.Contains(parsed.Path, "/") {
			return &URLLiteral{
				NodeBase: NodeBase{
					Span: span,
				},
				Value: _url,
			}
		}

		var parsingErr *ParsingError

		if strings.Contains(_url, "?") {
			parsingErr = &ParsingError{
				"HTTP host literals cannot contain a query part",
				i,
				start,
				URLlike,
				nil,
			}
		}

		return &HTTPHostLiteral{
			NodeBase: NodeBase{
				Span: span,
				Err:  parsingErr,
			},
			Value: _url,
		}
	}

	parseIdentLike := func() Node {
		start := i
		i++
		for i < len(s) && isIdentChar(s[i]) {
			i++
		}

		name := string(s[start:i])
		ident := &IdentifierLiteral{
			NodeBase: NodeBase{
				Span: NodeSpan{start, i},
			},
			Name: name,
		}

		if i < len(s) && s[i] == '.' {
			i++

			memberExpr := &IdentifierMemberExpression{
				NodeBase: NodeBase{
					Span: NodeSpan{Start: ident.Span.Start},
				},
				Left:          ident,
				PropertyNames: nil,
			}

			for {
				start := i

				if i >= len(s) {
					memberExpr.NodeBase.Span.End = len(s)
					memberExpr.NodeBase.Err = &ParsingError{
						"unterminated identifier member expression",
						i,
						start,
						KnownType,
						(*IdentifierMemberExpression)(nil),
					}

					return memberExpr
				}

				if !isAlpha(s[i]) && s[i] != '_' {
					memberExpr.NodeBase.Err = &ParsingError{
						"property name should start with a letter not '" + string(s[i]) + "'",
						i,
						start,
						IdentLike,
						(*IdentifierMemberExpression)(nil),
					}
					return memberExpr
				}

				for i < len(s) && isAlpha(s[i]) {
					i++
				}

				propName := string(s[start:i])

				memberExpr.PropertyNames = append(memberExpr.PropertyNames, &IdentifierLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						nil,
						nil,
					},
					Name: propName,
				})
				if i >= len(s) || s[i] != '.' {
					break
				}
				i++
			}

			memberExpr.Span.End = i
			return memberExpr
		}

		switch name {
		case "true", "false":
			return &BooleanLiteral{
				NodeBase: NodeBase{
					Span: ident.Span,
				},
				Value: name[0] == 't',
			}
		case "nil":
			return &NilLiteral{
				NodeBase: NodeBase{
					Span: ident.Span,
				},
			}
		case "require":
			panic(ParsingError{
				"require is a keyword, it cannot be used as an identifier",
				i,
				start,
				UnspecifiedCategory,
				nil,
			})
		case "http", "https":
			if i < len(s)-2 && string(s[i:i+3]) == "://" {
				return parseURLLike(start)
			}
		}

		if i < len(s) && strings.HasPrefix(string(s[i:]), "://") {
			base := ident.NodeBase
			base.Err = &ParsingError{
				"invalid URI : unsupported protocol",
				i,
				start,
				URLlike,
				nil,
			}

			return &InvalidURL{
				NodeBase: base,
				Value:    name,
			}
		}

		return ident
	}

	parseKeyList := func() *KeyListExpression {
		start := i
		i += 2

		var idents []*IdentifierLiteral

		for i < len(s) && s[i] != '}' {
			eatSpaceComma()

			if i >= len(s) {
				//this case is handled next
				break
			}

			e, missingExpr := parseExpression()
			if missingExpr {
				continue
			}

			if ident, ok := e.(*IdentifierLiteral); ok {
				idents = append(idents, ident)
			} else {
				panic(ParsingError{
					"a key list can only contain identifiers",
					i,
					start,
					KnownType,
					(*KeyListExpression)(nil),
				})
			}

			eatSpaceComma()
		}

		var parsingErr *ParsingError

		if i >= len(s) {
			parsingErr = &ParsingError{
				"unterminated key list, missing closing brace '}'",
				i,
				start,
				KnownType,
				(*KeyListExpression)(nil),
			}
		}
		i++

		return &KeyListExpression{
			NodeBase: NodeBase{
				NodeSpan{start, i},
				parsingErr,
				nil,
			},
			Keys: idents,
		}
	}

	var parseComplexPatternStuff func(bool) Node

	parsePatternPiece := func() Node {
		start := i
		patternKind := UnspecifiedPatternKind

		var parsingErr *ParsingError

		if isAlpha(s[i]) {
			for i < len(s) && isIdentChar(s[i]) {
				i++
			}

			patternKindName := string(s[start:i])

			switch patternKindName {
			case "int":
				patternKind = IntegerPattern
			case "float":
				patternKind = FloatPattern
			case "string":
				patternKind = StringPattern
			default:
				parsingErr = &ParsingError{
					fmt.Sprintf("invalid pattern kind: '%s'", patternKindName),
					i,
					start,
					UnspecifiedCategory,
					nil,
				}
			}

			eatSpace()
			if i >= len(s) {
				parsingErr = &ParsingError{
					fmt.Sprintf("invalid pattern piece: the kind '%s' should be followed elements of the pattern", patternKindName),
					i,
					start,
					UnspecifiedCategory,
					nil,
				}
			}

		}

		var elements []*PatternPieceElement

		for i < len(s) && s[i] != ';' && s[i] != '|' && s[i] != ')' {
			eatSpace()
			if i >= len(s) || s[i] == ';' || s[i] == '|' || s[i] == ')' {
				continue
			}

			var element Node
			elementStart := i
			if s[i] == '(' {
				i++

				eatSpace()

				if i >= len(s) {

					panic(ParsingError{
						fmt.Sprintf("unterminated parenthesized pattern"),
						i,
						start,
						UnspecifiedCategory,
						nil,
					})
				}
				element = parseComplexPatternStuff(true)

				eatSpace()

				if i >= len(s) || s[i] != ')' {
					parsingErr = &ParsingError{
						fmt.Sprintf("unterminated parenthesized pattern, missing closing parenthesis"),
						i,
						start,
						UnspecifiedCategory,
						nil,
					}
					break
				}
				i++
			} else {
				element = parseComplexPatternStuff(true)
			}

			ocurrenceModifier := ExactlyOneOcurrence
			count := 0
			elementEnd := i

			var elemParsingErr *ParsingError

			if i < len(s) && (s[i] == '+' || s[i] == '*' || s[i] == '?' || s[i] == '=') {
				switch s[i] {
				case '+':
					ocurrenceModifier = AtLeastOneOcurrence
					elementEnd++
					i++
				case '*':
					ocurrenceModifier = ZeroOrMoreOcurrence
					elementEnd++
					i++
				case '?':
					ocurrenceModifier = OptionalOcurrence
					elementEnd++
					i++
				case '=':
					i++
					numberStart := i
					if i >= len(s) || !isDigit(s[i]) {
						elemParsingErr = &ParsingError{
							fmt.Sprintf("unterminated pattern: unterminated exact ocurrence count: missing count after '='"),
							i,
							start,
							KnownType,
							(*PatternPieceElement)(nil),
						}
						elementEnd = i
						goto after_ocurrence
					}

					for i < len(s) && isDigit(s[i]) {
						i++
					}

					_count, err := strconv.ParseUint(string(s[numberStart:i]), 10, 32)
					if err != nil {
						elemParsingErr = &ParsingError{
							fmt.Sprintf("invalid pattern: invalid exact ocurrence count"),
							i,
							start,
							KnownType,
							(*PatternPieceElement)(nil),
						}
					}
					count = int(_count)
					ocurrenceModifier = ExactOcurrence
					elementEnd = i
				}
			}

		after_ocurrence:
			elements = append(elements, &PatternPieceElement{
				NodeBase: NodeBase{
					NodeSpan{elementStart, elementEnd},
					elemParsingErr,
					nil,
				},
				Ocurrence:           ocurrenceModifier,
				ExactOcurrenceCount: int(count),
				Expr:                element,
			})
		}

		return &PatternPiece{
			NodeBase: NodeBase{
				NodeSpan{start, i},
				parsingErr,
				nil,
			},
			Kind:     patternKind,
			Elements: elements,
		}
	}

	parseComplexPatternStuff = func(inPattern bool) Node {
		start := i

		if i >= len(s) {
			before := string(s[max(0, i-5):max(i, len(s))])

			return &InvalidComplexPatternElement{
				NodeBase: NodeBase{
					NodeSpan{start, i},
					&ParsingError{
						fmt.Sprintf("a pattern was expected: ...%s<<here>>", before),
						i,
						start,
						UnspecifiedCategory,
						nil,
					},
					nil,
				},
			}
		}

		if inPattern {
			switch {
			case isAlpha(s[i]) || s[i] == '(':
				return parsePatternPiece()
			case s[i] == '"' || s[i] == '\'':
				e, _ := parseExpression()
				return e
			case s[i] == '|':
				var cases []Node

				for i < len(s) && s[i] != ';' && s[i] != ')' {
					eatSpace()
					if i >= len(s) || s[i] == ';' || s[i] == ')' {
						continue
					}

					if s[i] != '|' {

						for i < len(s) && s[i] != ';' && s[i] != ')' {
							i++
						}

						return &PatternUnion{
							NodeBase: NodeBase{
								NodeSpan{start, i},
								&ParsingError{
									"invalid pattern union : elements should be separated by '|'",
									i,
									start,
									UnspecifiedCategory,
									nil,
								},
								nil,
							},
							Cases: cases,
						}
					}
					i++

					eatSpace()

					case_ := parseComplexPatternStuff(true)
					cases = append(cases, case_)
				}

				return &PatternUnion{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						nil,
						nil,
					},
					Cases: cases,
				}
			}
		}

		if s[i] == '%' {
			i++
			if i >= len(s) {
				return &InvalidComplexPatternElement{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						&ParsingError{
							"unterminated pattern: '%'",
							i,
							start,
							UnspecifiedCategory,
							nil,
						},
						nil,
					},
				}
			}

			switch {
			case isIdentChar(s[i]): //pattern identifier literal

				for i < len(s) && isIdentChar(s[i]) {
					i++
				}

				left := &PatternIdentifierLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						nil,
						nil,
					},
					Name: string(s[start+1 : i]),
				}

				eatSpace()

				if i >= len(s) || s[i] != '=' || inPattern {
					return left
				}

				i++
				eatSpace()

				right := parseComplexPatternStuff(true)

				eatSpace()

				var parsingErr *ParsingError

				if i < len(s) && s[i] == ';' {
					i++
				}

				return &PatternDefinition{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						parsingErr,
						nil,
					},
					Left:  left,
					Right: right,
				}
			case s[i] == '{': //object pattern literal
				openingBraceIndex := i
				i++

				unamedPropCount := 0
				var properties []ObjectProperty

			top_object_pattern_loop:
				for i < len(s) && s[i] != '}' {
					eatSpaceNewlineComma()

					var objectPropertyErr *ParsingError

					if i < len(s) && s[i] == '}' {
						break
					}

					var keys []Node //example of multiple keys: {a,b: 1}
					var lastKey Node = nil
					lastKeyName := ""
					var propSpanStart int

					if s[i] == ':' {
						propSpanStart = i
						i++
						unamedPropCount++
						keys = append(keys, nil)
						lastKeyName = strconv.Itoa(unamedPropCount)
						if len(lastKeyName) > MAX_OBJECT_KEY_BYTE_LEN {
							objectPropertyErr = &ParsingError{
								"key is too long",
								i,
								openingBraceIndex,
								KnownType,
								(*ObjectPatternLiteral)(nil),
							}
						}
					} else {
						for {
							lastKey, _ = parseExpression()

							keys = append(keys, lastKey)

							switch k := lastKey.(type) {
							case *IdentifierLiteral:
								lastKeyName = k.Name
							case *StringLiteral:
								lastKeyName = k.Value
							default:
								objectPropertyErr = &ParsingError{
									"Only identifiers and strings are valid object pattern keys",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectPatternLiteral)(nil),
								}
							}

							if len(lastKeyName) > MAX_OBJECT_KEY_BYTE_LEN {
								objectPropertyErr = &ParsingError{
									"key is too long",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectPatternLiteral)(nil),
								}
							}

							if len(keys) == 1 {
								propSpanStart = lastKey.Base().Span.Start
							}
							singleKey := true

							eatSpace()

							if s[i] == ',' {
								i++
								eatSpace()
								singleKey = false
							}

							if i >= len(s) || s[i] == '}' {
								properties = append(properties, ObjectProperty{
									NodeBase: NodeBase{
										Span: NodeSpan{propSpanStart, i},
										Err: &ParsingError{
											"invalid object pattern literal, missing colon after key '" + lastKeyName + "'",
											i,
											openingBraceIndex,
											KnownType,
											(*ObjectPatternLiteral)(nil),
										},
									},
									Key:   lastKey,
									Value: nil,
								})
								break top_object_pattern_loop
							}

							if singleKey {
								if s[i] != ':' {
									properties = append(properties, ObjectProperty{
										NodeBase: NodeBase{
											Span: NodeSpan{propSpanStart, i},
											Err: &ParsingError{
												"invalid object pattern literal, following key should be followed by a colon : '" + lastKeyName + "'",
												i,
												openingBraceIndex,
												KnownType,
												(*ObjectPatternLiteral)(nil),
											},
										},
										Key:   lastKey,
										Value: nil,
									})

									continue top_object_pattern_loop
								}
								i++
								break
							}
						}
					}

					eatSpace()

					if i >= len(s) || s[i] == '}' {
						properties = append(properties, ObjectProperty{
							NodeBase: NodeBase{
								Span: NodeSpan{propSpanStart, i},
								Err: &ParsingError{
									"invalid object pattern literal, missing value after colon, key '" + lastKeyName + "'",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectPatternLiteral)(nil),
								},
							},
							Key:   lastKey,
							Value: nil,
						})

						continue top_object_pattern_loop
					}

					value, _ := parseExpression()

					if i >= len(s) {
						return &ObjectPatternLiteral{
							NodeBase: NodeBase{
								Span: NodeSpan{openingBraceIndex - 1, i},
								Err: &ParsingError{
									"unterminated object pattern literal, missing closing brace '}'",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectPatternLiteral)(nil),
								},
							},
							Properties: properties,
						}

						continue top_object_pattern_loop
					}

					if len(keys) > 1 {
						switch value.(type) {
						case *Variable, *GlobalVariable:
						default:
							if !IsSimpleValueLiteral(value) {
								objectPropertyErr = &ParsingError{
									"invalid object pattern literal, the value of a multi-key property definition should be a simple literal or a variable, last key is '" + lastKeyName + "'",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectPatternLiteral)(nil),
								}
							}
						}

					}

					for _, key := range keys {
						properties = append(properties, ObjectProperty{
							NodeBase: NodeBase{
								Span: NodeSpan{propSpanStart, i},
								Err:  objectPropertyErr,
							},
							Key:   key,
							Value: value,
						})
					}

					eatSpaceNewlineComma()
				}

				var parsingErr *ParsingError
				if i >= len(s) {
					parsingErr = &ParsingError{
						"unterminated object pattern literal, missing closing brace '}'",
						i,
						openingBraceIndex,
						KnownType,
						(*ObjectPatternLiteral)(nil),
					}
				} else {
					i++
				}

				return &ObjectPatternLiteral{
					NodeBase: NodeBase{
						Span: NodeSpan{openingBraceIndex - 1, i},
						Err:  parsingErr,
					},
					Properties: properties,
				}
			case s[i] == '[': //list pattern literal

				openingBracketIndex := i
				i++

				var elements []Node
				for i < len(s) && s[i] != ']' {
					eatSpaceNewlineComma()

					if i < len(s) && s[i] == ']' {
						break
					}

					e, isMissingExpr := parseExpression()
					if i >= len(s) || (isMissingExpr && s[i] != ',') {
						break
					} else if !isMissingExpr {
						elements = append(elements, e)
					}

					eatSpaceNewlineComma()
				}

				var parsingErr *ParsingError

				if i >= len(s) || s[i] != ']' {
					parsingErr = &ParsingError{
						"unterminated list pattern literal, missing closing bracket ']'",
						i,
						openingBracketIndex,
						KnownType,
						(*ListPatternLiteral)(nil),
					}
				} else {
					i++
				}

				return &ListPatternLiteral{
					NodeBase: NodeBase{
						Span: NodeSpan{openingBracketIndex - 1, i},
						Err:  parsingErr,
					},
					Elements: elements,
				}
			case s[i] == '"':
				e, _ := parseExpression()
				str := e.(*StringLiteral)
				return &RegularExpressionLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, str.Base().Span.End},
						str.Err,
						nil,
					},
					Raw:   str.Raw,
					Value: str.Value,
				}
			default:
				return &InvalidComplexPatternElement{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						&ParsingError{
							"unterminated pattern: '%'",
							i,
							start,
							UnspecifiedCategory,
							nil,
						},
						nil,
					},
				}
			}
		}

		left := string(s[max(0, i-5):i])
		right := string(s[i:min(len(s), i+5)])

		return &InvalidComplexPatternElement{
			NodeBase: NodeBase{
				NodeSpan{start, i},
				&ParsingError{
					fmt.Sprintf("a pattern was expected: ...%s<<here>>%s...", left, right),
					i,
					start,
					UnspecifiedCategory,
					nil,
				},
				nil,
			},
		}
	}

	parseExpression = func() (Node, bool) {
		__start := i
		//these variables are only used for expressions that can be on the left of a member/slice/index/call expression
		//other expressions are directly returned
		var lhs Node
		var first Node
		var parenthesizedFirstStart int

		if i >= len(s) {
			return &MissingExpression{
				NodeBase: NodeBase{
					Span: NodeSpan{i - 1, i},
					Err: &ParsingError{
						fmt.Sprintf("an expression was expected: ...%s<<here>>", string(s[max(0, i-5):i])),
						i,
						i - 1,
						UnspecifiedCategory,
						nil,
					},
				},
			}, true
		}

		switch s[i] {
		case '$': //normal & global variables
			start := i
			isGlobal := false
			i++

			if i < len(s) && s[i] == '$' {
				isGlobal = true
				i++
			}

			for i < len(s) && isIdentChar(s[i]) {
				i++
			}

			if isGlobal {
				lhs = &GlobalVariable{
					NodeBase: NodeBase{
						Span: NodeSpan{start, i},
					},
					Name: string(s[start+2 : i]),
				}
			} else {
				lhs = &Variable{
					NodeBase: NodeBase{
						Span: NodeSpan{start, i},
					},
					Name: string(s[start+1 : i]),
				}
			}

			if i < len(s) && s[i] == '?' {
				i++
				lhs = &BooleanConversionExpression{
					NodeBase: NodeBase{
						NodeSpan{__start, i},
						nil,
						nil,
					},
					Expr: lhs,
				}
			}

		//TODO: refactor ?
		case '_', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z':
			identLike := parseIdentLike()
			spawnExprStart := identLike.Base().Span.Start
			tokens := make([]ValuelessToken, 0)
			var name string

			switch v := identLike.(type) {
			case *IdentifierLiteral:
				name = v.Name
			case *IdentifierMemberExpression:
				name = v.Left.Name
			default:
				return v, false
			}

			if name == "sr" {
				tokens = append(tokens, ValuelessToken{SPAWN_KEYWORD, identLike.Base().Span})
				eatSpace()
				if i >= len(s) {
					panic(ParsingError{
						"invalid spawn expression: sr should be followed by two expressions",
						i,
						spawnExprStart,
						KnownType,
						(*SpawnExpression)(nil),
					})
				}

				var routineGroupIdent *IdentifierLiteral
				var globals Node
				e, missingExpr := parseExpression()

				switch ev := e.(type) {
				case *IdentifierLiteral: //if there is a group name the globals' object is the next expression
					routineGroupIdent = ev
					eatSpace()

					globals, missingExpr = parseExpression()
					eatSpace()
				case *MissingExpression:
				default:
					globals = e
				}

				eatSpace()

				if i >= len(s) || missingExpr {
					return &SpawnExpression{
						NodeBase: NodeBase{
							NodeSpan{identLike.Base().Span.Start, i},
							&ParsingError{
								"invalid spawn expression: sr should be followed by two expressions",
								i,
								spawnExprStart,
								KnownType,
								(*SpawnExpression)(nil),
							},
							tokens,
						},
						GroupIdent: routineGroupIdent,
						Globals:    globals,
					}, false
				}

				var expr Node
				var parsingErr *ParsingError

				if s[i] == '{' { //embedded module: sr ... { <embedded module> }
					start := i
					i++
					emod := &EmbeddedModule{}

					var stmts []Node

					eatSpace()
					requirements := parseRequirements()

					eatSpaceNewLineSemiColonComment()

					for i < len(s) && s[i] != '}' {
						stmt := parseStatement()
						if _, isMissingExpr := stmt.(*MissingExpression); isMissingExpr {
							if isMissingExpr {
								i++

								if i >= len(s) {
									stmts = append(stmts, stmt)
									break
								}
							}
						}
						stmts = append(stmts, stmt)
						eatSpaceNewLineSemiColonComment()
					}

					var embeddedModuleErr *ParsingError

					if i >= len(s) || s[i] != '}' {
						embeddedModuleErr = &ParsingError{
							"unterminated embedded module",
							i,
							start,
							KnownType,
							(*EmbeddedModule)(nil),
						}
					} else {
						i++
					}

					emod.Requirements = requirements
					emod.Statements = stmts
					emod.NodeBase = NodeBase{
						NodeSpan{start, i},
						embeddedModuleErr,
						nil,
					}
					expr = emod
				} else {
					expr, missingExpr = parseExpression()
					if missingExpr {
						parsingErr = &ParsingError{
							"invalid spawn expression: ",
							i,
							spawnExprStart,
							KnownType,
							(*EmbeddedModule)(nil),
						}
					}
				}

				eatSpace()
				var grantedPermsLit *ObjectLiteral

				if i < len(s) && s[i] == 'a' {
					allowIdent, _ := parseExpression()
					if ident, ok := allowIdent.(*IdentifierLiteral); !ok || ident.Name != "allow" {

						parsingErr = &ParsingError{
							"spawn expression: argument should be followed by a the 'allow' keyword",
							i,
							spawnExprStart,
							KnownType,
							(*SpawnExpression)(nil),
						}
					} else { //if ok
						tokens = append(tokens, ValuelessToken{ALLOW_KEYWORD, allowIdent.Base().Span})

						eatSpace()

						grantedPerms, _ := parseExpression()
						var ok bool
						grantedPermsLit, ok = grantedPerms.(*ObjectLiteral)
						if !ok {
							parsingErr = &ParsingError{
								"spawn expression: 'allow' keyword should be followed by an object literal (permissions)",
								i,
								spawnExprStart,
								KnownType,
								(*SpawnExpression)(nil),
							}
						}
					}

				}

				return &SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{identLike.Base().Span.Start, i},
						parsingErr,
						tokens,
					},
					GroupIdent:         routineGroupIdent,
					Globals:            globals,
					ExprOrVar:          expr,
					GrantedPermissions: grantedPermsLit,
				}, false
			}

			if name == "fn" {
				return parseFunction(identLike.Base().Span.Start), false
			}

			if i >= len(s) {
				return identLike, false
			}

			switch {
			case s[i] == '"': //func_name"string"
				call := &Call{
					NodeBase: NodeBase{
						Span: NodeSpan{identLike.Base().Span.Start, 0},
					},
					Callee:    identLike,
					Arguments: nil,
					Must:      true,
				}

				str, _ := parseExpression()
				call.Arguments = append(call.Arguments, str)
				call.NodeBase.Span.End = str.Base().Span.End
				return call, false
			case s[i] == '(' && !isKeyword(name): //func_name(...
				i++
				eatSpace()

				call := &Call{
					NodeBase: NodeBase{
						NodeSpan{identLike.Base().Span.Start, 0},
						nil,
						nil,
					},
					Callee:    identLike,
					Arguments: nil,
				}

				for i < len(s) && s[i] != ')' {
					eatSpaceNewlineComma()
					arg, _ := parseExpression()

					if i >= len(s) {
						call.Err = &ParsingError{
							"untermianted call: 'allow' keyword should be followed by an object literal (permissions)",
							i,
							spawnExprStart,
							KnownType,
							(*SpawnExpression)(nil),
						}
					}

					call.Arguments = append(call.Arguments, arg)
					eatSpaceNewlineComma()
				}

				if i < len(s) {
					i++
				}

				if i < len(s) && s[i] == '!' {
					call.Must = true
					i++
				}

				call.NodeBase.Span.End = i

				return call, false
			case s[i] == '$': //funcname$ ...
				i++

				call := &Call{
					NodeBase: NodeBase{
						Span: NodeSpan{identLike.Base().Span.Start, 0},
					},
					Callee:    identLike,
					Arguments: nil,
					Must:      true,
				}

				if i >= len(s) || (s[i] != '\t' && s[i] != ' ') {
					call.Err = &ParsingError{
						"a non-parenthesized call expression should have arguments and the callee (<name>$) should be followed by a space",
						i,
						identLike.Base().Span.Start,
						KnownType,
						(*Call)(nil),
					}
					return call, false
				}

				for i < len(s) && s[i] != '\n' && !isNotPairedOrIsClosingDelim(s[i]) {
					eatSpaceAndComments()

					if s[i] == '\n' || isNotPairedOrIsClosingDelim(s[i]) {
						break
					}

					arg, _ := parseExpression()

					call.Arguments = append(call.Arguments, arg)
					eatSpaceAndComments()
				}

				if i < len(s) && s[i] == '\n' {
					i++
				}

				call.NodeBase.Span.End = call.Arguments[len(call.Arguments)-1].Base().Span.End
				return call, false
			}

			return identLike, false
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9': //integers and floating point numbers
			start := i
			var parsingErr *ParsingError

			parseIntegerLiteral := func(raw string, start, end int) (*IntLiteral, int64) {
				integer, err := strconv.ParseInt(raw, 10, 32)
				if err != nil {
					parsingErr = &ParsingError{
						"invalid integer literal '" + raw + "'",
						end,
						start,
						KnownType,
						(*IntLiteral)(nil),
					}
				}

				return &IntLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, end},
						parsingErr,
						nil,
					},
					Raw:   raw,
					Value: int(integer),
				}, integer
			}

			for i < len(s) && isDigit(s[i]) {
				i++
			}

			if i < len(s) && s[i] == '.' {
				i++

				if i < len(s) && s[i] == '.' { //int range literal
					lower := string(s[start : i-1])
					lowerIntLiteral, _ := parseIntegerLiteral(lower, start, i-1)

					i++
					if i >= len(s) || !isDigit(s[i]) {
						return &IntegerRangeLiteral{
							NodeBase: NodeBase{
								NodeSpan{start, i},
								&ParsingError{
									"unterminated integer range literal '" + string(s[start:i]) + "'",
									i,
									start,
									KnownType,
									(*IntLiteral)(nil),
								},
								nil,
							},
							LowerBound: nil,
							UpperBound: nil,
						}, false
					}

					upperStart := i

					for i < len(s) && isDigit(s[i]) {
						i++
					}

					upper := string(s[upperStart:i])

					upperIntLiteral, _ := parseIntegerLiteral(upper, upperStart, i)
					return &IntegerRangeLiteral{
						NodeBase: NodeBase{
							NodeSpan{lowerIntLiteral.Base().Span.Start, upperIntLiteral.Base().Span.End},
							nil,
							nil,
						},
						LowerBound: lowerIntLiteral,
						UpperBound: upperIntLiteral,
					}, false
				}

				for i < len(s) && (isDigit(s[i]) || s[i] == '-') {
					i++
				}
			}

			raw := string(s[start:i])

			var literal Node
			var fValue float64

			if strings.ContainsRune(raw, '.') {

				float, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					parsingErr = &ParsingError{
						"invalid floating point literal '" + raw + "'",
						i,
						start,
						KnownType,
						(*FloatLiteral)(nil),
					}
				}

				literal = &FloatLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						parsingErr,
						nil,
					},
					Raw:   raw,
					Value: float,
				}

				fValue = float
			} else {
				var integer int64
				literal, integer = parseIntegerLiteral(raw, start, i)
				fValue = float64(integer)
			}

			if i < len(s) && (isAlpha(s[i]) || s[i] == '%') {
				unitStart := i

				i++

				for i < len(s) && isAlpha(s[i]) {
					i++
				}

				raw = string(s[start:i])
				unit := string(s[unitStart:i])

				literal = &QuantityLiteral{
					NodeBase: NodeBase{
						Span: NodeSpan{literal.Base().Span.Start, i},
					},
					Raw:   raw,
					Value: fValue,
					Unit:  unit,
				}

				if i < len(s) {
					switch s[i] {
					case '/':
						i++
						var ident *IdentifierLiteral
						unit, isMissingExpr := parseExpression()

						if isMissingExpr {
							parsingErr = &ParsingError{
								"invalid rate literal",
								i,
								start,
								KnownType,
								(*IntLiteral)(nil),
							}
						}

						ident, ok := unit.(*IdentifierLiteral)
						raw := string(s[start:i])

						if !ok {
							parsingErr = &ParsingError{
								"invalid rate literal '" + raw + "', '/' should be immeditately followed by an identifier ('s' for example)",
								i,
								start,
								KnownType,
								(*IntLiteral)(nil),
							}
						}

						return &RateLiteral{
							NodeBase: NodeBase{
								NodeSpan{literal.Base().Span.Start, ident.Base().Span.End},
								parsingErr,
								nil,
							},
							Quantity: literal.(*QuantityLiteral),
							Unit:     ident,
						}, false
					}
				}
			}

			return literal, false

		case '{': //object
			openingBraceIndex := i
			i++

			unamedPropCount := 0
			var properties []ObjectProperty
			var spreadElements []*PropertySpreadElement
			var invalidElements []*InvalidObjectElement
			var parsingErr *ParsingError

		object_literal_top_loop:
			for i < len(s) && s[i] != '}' {
				var elementParsingErr *ParsingError
				eatSpaceAndNewLineAndCommaAndComment()

				if i < len(s) && s[i] == '}' {
					break
				}

				var keys []Node //example of multiple keys: {a,b: 1}
				var lastKey Node = nil
				lastKeyName := ""
				var propSpanStart int

				if s[i] == '.' { //spread element
					spreadStart := i

					if string(s[i:min(len(s), i+3)]) != "..." {

						for i < len(s) && s[i] != '}' && s[i] != ',' {
							invalidElements = append(invalidElements, &InvalidObjectElement{
								NodeBase: NodeBase{
									NodeSpan{spreadStart, i},
									&ParsingError{
										"invalid element in object literal",
										i,
										openingBraceIndex,
										KnownType,
										(*ObjectLiteral)(nil),
									},
									nil,
								},
							})

							eatSpace()
							continue object_literal_top_loop
						}
					}

					i += 3
					eatSpace()

					expr, _ := parseExpression()

					extractionExpr, ok := expr.(*ExtractionExpression)
					if !ok {
						elementParsingErr = &ParsingError{
							fmt.Sprintf("invalid spread element in object literal : expression should be an extraction expression not a(n) %T", expr),
							i,
							openingBraceIndex,
							KnownType,
							(*ObjectLiteral)(nil),
						}
					}

					spreadElements = append(spreadElements, &PropertySpreadElement{
						NodeBase: NodeBase{
							NodeSpan{spreadStart, extractionExpr.Span.End},
							elementParsingErr,
							nil,
						},
						Extraction: extractionExpr,
					})

				} else {
					if s[i] == ':' { //implicit key
						propSpanStart = i
						i++
						unamedPropCount++
						keys = append(keys, nil)
						lastKeyName = strconv.Itoa(unamedPropCount)
						if len(lastKeyName) > MAX_OBJECT_KEY_BYTE_LEN {
							panic(ParsingError{
								"key is too long",
								i,
								openingBraceIndex,
								KnownType,
								(*ObjectLiteral)(nil),
							})

						}
					} else { //explicit key(s)

						//shared value properties
						for {
							lastKey, _ = parseExpression()
							keys = append(keys, lastKey)

							switch k := lastKey.(type) {
							case *IdentifierLiteral:
								lastKeyName = k.Name
							case *StringLiteral:
								lastKeyName = k.Value
							default:
								panic(ParsingError{
									"Only identifiers and strings are valid object keys",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectLiteral)(nil),
								})
							}

							if len(lastKeyName) > MAX_OBJECT_KEY_BYTE_LEN {
								panic(ParsingError{
									"key is too long",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectLiteral)(nil),
								})
							}

							if len(keys) == 1 {
								propSpanStart = lastKey.Base().Span.Start
							}
							singleKey := true

							eatSpace()

							if s[i] == ',' {
								i++
								eatSpace()
								singleKey = false
							}

							if i >= len(s) || s[i] == '}' {
								panic(ParsingError{
									"invalid object literal, missing colon after key '" + lastKeyName + "'",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectLiteral)(nil),
								})
							}

							if singleKey {
								if s[i] != ':' {
									panic(ParsingError{
										"invalid object literal, following key should be followed by a colon : '" + lastKeyName + "'",
										i,
										openingBraceIndex,
										KnownType,
										(*ObjectLiteral)(nil),
									})
								}
								i++
								break
							}
						}

					}

					eatSpace()

					if i >= len(s) || s[i] == '}' {
						properties = append(properties, ObjectProperty{
							NodeBase: NodeBase{
								Span: NodeSpan{propSpanStart, i},
								Err: &ParsingError{
									"invalid object pattern literal, missing value after colon, key '" + lastKeyName + "'",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectLiteral)(nil),
								},
							},
							Key:   lastKey,
							Value: nil,
						})

						continue object_literal_top_loop
					}
					v, _ := parseExpression()

					if len(keys) > 1 {
						switch v.(type) {
						case *Variable, *GlobalVariable:
						default:
							if !IsSimpleValueLiteral(v) {
								elementParsingErr = &ParsingError{
									"invalid object pattern literal, the value of a multi-key property definition should be a simple literal or a variable, last key is '" + lastKeyName + "'",
									i,
									openingBraceIndex,
									KnownType,
									(*ObjectLiteral)(nil),
								}
							}
						}

					}

					for _, key := range keys {
						properties = append(properties, ObjectProperty{
							NodeBase: NodeBase{
								Span: NodeSpan{propSpanStart, i},
								Err:  elementParsingErr,
							},
							Key:   key,
							Value: v,
						})
					}
				}

				eatSpaceAndNewLineAndCommaAndComment()
			}

			if i >= len(s) {
				parsingErr = &ParsingError{
					"unterminated object literal, missing closing brace '}'",
					i,
					openingBraceIndex,
					KnownType,
					(*ObjectLiteral)(nil),
				}
			} else {
				i++
			}

			return &ObjectLiteral{
				NodeBase: NodeBase{
					Span: NodeSpan{openingBraceIndex, i},
					Err:  parsingErr,
				},
				Properties:     properties,
				SpreadElements: spreadElements,
			}, false
		case '[': //list
			openingBracketIndex := i
			i++

			var elements []Node
			for i < len(s) && s[i] != ']' {
				eatSpaceNewlineComma()

				if i < len(s) && s[i] == ']' {
					break
				}

				e, isMissingExpr := parseExpression()
				if i >= len(s) || (isMissingExpr && s[i] != ',') {
					break
				} else if !isMissingExpr {
					elements = append(elements, e)
				}

				eatSpaceNewlineComma()
			}

			var parsingErr *ParsingError

			if i >= len(s) || s[i] != ']' {
				parsingErr = &ParsingError{
					"unterminated list literal, missing closing bracket ']'",
					i,
					openingBracketIndex,
					KnownType,
					(*ListLiteral)(nil),
				}
			} else {
				i++
			}

			return &ListLiteral{
				NodeBase: NodeBase{
					Span: NodeSpan{openingBracketIndex, i},
					Err:  parsingErr,
				},
				Elements: elements,
			}, false
		case '\'': //rune | rune range literal
			start := i

			parseRuneLiteral := func() *RuneLiteral {
				start := i
				i++

				if i >= len(s) {
					return &RuneLiteral{
						NodeBase: NodeBase{
							NodeSpan{start, i},
							&ParsingError{
								"unterminated rune literal",
								i,
								start,
								KnownType,
								(*RuneLiteral)(nil),
							},
							nil,
						},
						Value: 0,
					}
				}

				value := s[i]

				if value == '\'' {
					return &RuneLiteral{
						NodeBase: NodeBase{
							NodeSpan{start, i},
							&ParsingError{
								"invalid rune literal : no character",
								i,
								start,
								KnownType,
								(*RuneLiteral)(nil),
							},
							nil,
						},
						Value: 0,
					}
				}

				if value == '\\' {
					i++
					switch s[i] {
					//same single character escapes as Golang
					case 'a':
						value = '\a'
					case 'b':
						value = '\b'
					case 'f':
						value = '\f'
					case 'n':
						value = '\n'
					case 'r':
						value = '\r'
					case 't':
						value = '\t'
					case 'v':
						value = '\v'
					case '\\':
						value = '\\'
					case '\'':
						value = '\''
					default:
						return &RuneLiteral{
							NodeBase: NodeBase{
								NodeSpan{start, i},
								&ParsingError{
									"invalid rune literal: invalid single character escape" + string(s[start:i]),
									i,
									start,
									KnownType,
									(*RuneLiteral)(nil),
								},
								nil,
							},
							Value: 0,
						}
					}
				}

				i++

				var parsingErr *ParsingError
				if i >= len(s) || s[i] != '\'' {
					parsingErr = &ParsingError{
						"unterminated rune literal, missing ' at the end",
						i,
						start,
						KnownType,
						(*RuneLiteral)(nil),
					}
				} else {
					i++
				}

				return &RuneLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						parsingErr,
						nil,
					},
					Value: value,
				}

			}

			lower := parseRuneLiteral()

			if i >= len(s) || s[i] != '.' {
				return lower, false
			}

			i++
			if i >= len(s) || s[i] != '.' {
				return &RuneRangeExpression{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						&ParsingError{
							"invalid rune range expression",
							i,
							start,
							KnownType,
							(*RuneRangeExpression)(nil),
						},
						nil,
					},
					Lower: lower,
					Upper: nil,
				}, false
			}
			i++

			if i >= len(s) || s[i] != '\'' {
				return &RuneRangeExpression{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						&ParsingError{
							"invalid rune range expression",
							i,
							start,
							KnownType,
							(*RuneRangeExpression)(nil),
						},
						nil,
					},
					Lower: lower,
					Upper: nil,
				}, false
			}

			upper := parseRuneLiteral()

			return &RuneRangeExpression{
				NodeBase: NodeBase{
					NodeSpan{start, upper.Base().Span.End},
					nil,
					nil,
				},
				Lower: lower,
				Upper: upper,
			}, false
		case '"': //string
			//strings are JSON strings
			start := i
			var parsingErr *ParsingError
			var value string
			var raw string

			i++

			for i < len(s) && (s[i] != '"' || countPrevBackslashes()%2 == 1) {
				i++
			}

			if i >= len(s) && s[i-1] != '"' {
				raw = string(s[start:])
				parsingErr = &ParsingError{
					"unterminated string literal '" + string(s[start:]) + "'",
					i,
					start,
					KnownType,
					(*StringLiteral)(nil),
				}
			} else {
				i++

				raw = string(s[start:i])
				err := json.Unmarshal([]byte(raw), &value)

				if err != nil {
					parsingErr = &ParsingError{
						"invalid string literal '" + raw + "': " + err.Error(),
						i,
						start,
						KnownType,
						(*StringLiteral)(nil),
					}
				}
			}

			return &StringLiteral{
				NodeBase: NodeBase{
					Span: NodeSpan{start, i},
					Err:  parsingErr,
				},
				Raw:   raw,
				Value: value,
			}, false
		case '/':
			return parsePathLikeExpression(false), false
		case '.':
			if i < len(s)-1 {
				if s[i+1] == '/' || i < len(s)-2 && s[i+1] == '.' && s[i+2] == '/' {
					return parsePathLikeExpression(false), false
				}
				switch s[i+1] {
				case '{':
					return parseKeyList(), false
				case '.':
					start := i
					i += 2

					upperBound, _ := parseExpression()
					expr := &UpperBoundRangeExpression{
						NodeBase: NodeBase{
							NodeSpan{start, i},
							nil,
							nil,
						},
						UpperBound: upperBound,
					}

					return expr, false
				}
			}
			i++
			return &UnknownNode{
				NodeBase: NodeBase{
					Span: NodeSpan{i - 1, i},
					Err: &ParsingError{
						"'.' should be followed by (.)?(/), or a letter",
						i,
						i - 1,
						UnspecifiedCategory,
						nil,
					},
				},
			}, false
		case '-': //options / flags
			i++
			if i >= len(s) {
				return &FlagLiteral{
					NodeBase: NodeBase{
						Span: NodeSpan{__start, i},
						Err: &ParsingError{
							"'-' should be followed an option name",
							i,
							__start,
							KnownType,
							(*FlagLiteral)(nil),
						},
					},
					SingleDash: true,
				}, false
			}

			singleDash := true

			if s[i] == '-' {
				singleDash = false
				i++
			}

			nameStart := i

			if i >= len(s) {
				return &FlagLiteral{
					NodeBase: NodeBase{
						Span: NodeSpan{__start, i},
						Err: &ParsingError{
							"'--' should be followed an option name",
							i,
							__start,
							KnownType,
							(*FlagLiteral)(nil),
						},
					},
					SingleDash: singleDash,
				}, false
			}

			if !isAlpha(s[i]) && !isDigit(s[i]) {
				return &FlagLiteral{
					NodeBase: NodeBase{
						Span: NodeSpan{__start, i},
						Err: &ParsingError{
							"the name of an option can only contain alphanumeric characters",
							i,
							__start,
							KnownType,
							(*FlagLiteral)(nil),
						},
					},
					SingleDash: singleDash,
				}, false
			}

			for i < len(s) && (isAlpha(s[i]) || isDigit(s[i])) {
				i++
			}

			name := string(s[nameStart:i])

			if i >= len(s) || s[i] != '=' {

				return &FlagLiteral{
					NodeBase: NodeBase{
						Span: NodeSpan{__start, i},
					},
					Name:       name,
					SingleDash: singleDash,
				}, false
			}

			i++

			if i >= len(s) {
				return &OptionExpression{
					NodeBase: NodeBase{
						Span: NodeSpan{__start, i},
						Err: &ParsingError{
							"unterminated option expression, '=' should be followed by an expression",
							i,
							__start,
							KnownType,
							(*OptionExpression)(nil),
						},
					},
					Name:       name,
					SingleDash: singleDash,
				}, false
			}

			value, _ := parseExpression()

			return &OptionExpression{
				NodeBase:   NodeBase{Span: NodeSpan{__start, i}},
				Name:       name,
				Value:      value,
				SingleDash: singleDash,
			}, false

		case '#':
			i++
			return &UnknownNode{
				NodeBase: NodeBase{
					Span: NodeSpan{i - 1, i},
					Err: &ParsingError{
						"",
						i,
						i - 1,
						UnspecifiedCategory,
						nil,
					},
				},
			}, false
		case '@': //lazy expressions & host related stuff
			start := i
			i++
			if i >= len(s) {
				return &UnknownNode{
					NodeBase: NodeBase{
						Span: NodeSpan{start, i},
						Err: &ParsingError{
							"'@' should be followed by '(' <expr> ')' or a host alias (@api/path)",
							i,
							start,
							UnspecifiedCategory,
							nil,
						},
					},
				}, false
			}

			if s[i] == '(' {
				//no increment on purpose

				e, _ := parseExpression()
				return &LazyExpression{
					NodeBase: NodeBase{
						Span: NodeSpan{start, i},
					},
					Expression: e,
				}, false
			} else if s[i] == '/' || (s[i] >= 'a' && s[i] <= 'z') {
				j := i
				i--

				for j < len(s) && isIdentChar(s[j]) {
					j++
				}

				aliasEndIndex := j

				for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
					j++
				}

				if j >= len(s) {
					i = j
					return &InvalidAliasRelatedNode{
						NodeBase: NodeBase{
							NodeSpan{start, j},
							&ParsingError{
								"unterminated AtHostLiteral | URLExpression | HostAliasDefinition",
								j,
								start,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
					}, false
				}

				//@alias = <host>
				if s[j] == '=' {

					left := &AtHostLiteral{
						NodeBase: NodeBase{
							NodeSpan{start, aliasEndIndex},
							nil,
							nil,
						},
						Value: string(s[start:aliasEndIndex]),
					}

					i = j + 1
					eatSpace()
					var parsingErr *ParsingError
					var right Node

					if i >= len(s) {
						parsingErr = &ParsingError{
							"unterminated HostAliasDefinition, missing value after '='",
							i,
							start,
							KnownType,
							(*HostAliasDefinition)(nil),
						}
					} else {
						right, _ = parseExpression()
					}

					return &HostAliasDefinition{
						NodeBase: NodeBase{
							NodeSpan{start, right.Base().Span.End},
							parsingErr,
							nil,
						},
						Left:  left,
						Right: right,
					}, false
				}

				return parseURLLike(start), false
			} else {

				return &UnknownNode{
					NodeBase: NodeBase{
						Span: NodeSpan{start, i},
						Err: &ParsingError{
							"'@' should be followed by '(' <expr> ')' or a host alias (@api/path)",
							i,
							start,
							UnspecifiedCategory,
							nil,
						},
					},
				}, false

			}
		case '%':
			if i < len(s)-1 && (s[i+1] == '.' || s[i+1] == '/') {
				i++
				return parsePathLikeExpression(true), false
			} else {
				return parseComplexPatternStuff(false), false
			}
		case '(': //parenthesized expression and binary expressions
			openingParenIndex := i
			i++
			left, _ := parseExpression()

			eatSpace()

			if i < len(s) && s[i] == ')' {
				i++
				lhs = left
				parenthesizedFirstStart = openingParenIndex
				break
			}

			UNTERMINATED_BIN_EXPR := "unterminated binary expression:"
			INVALID_BIN_EXPR := "invalid binary expression:"
			NON_EXISTING_OPERATOR := "invalid binary expression, non existing operator"

			if i >= len(s) {
				return &BinaryExpression{
					NodeBase: NodeBase{
						Span: NodeSpan{openingParenIndex, i},
						Err: &ParsingError{
							UNTERMINATED_BIN_EXPR + " missing operator",
							i,
							openingParenIndex,
							KnownType,
							(*BinaryExpression)(nil),
						},
					},
					Operator: -1,
					Left:     left,
				}, false
			}

			makeInvalidOperatorMissingRightOperand := func(operator BinaryOperator) Node {
				return &BinaryExpression{
					NodeBase: NodeBase{
						Span: NodeSpan{openingParenIndex, i},
						Err: &ParsingError{
							UNTERMINATED_BIN_EXPR + " missing right operand and/or invalid operator",
							i,
							openingParenIndex,
							KnownType,
							(*BinaryExpression)(nil),
						},
					},
					Operator: operator,
					Left:     left,
				}
			}

			eatInvalidOperator := func() {
				for i < len(s) && !isSpace(string(s[i])) && !isDelim(s[i]) && s[i] != '$' {
					i++
				}
			}

			var parsingErr *ParsingError

			var operator BinaryOperator
			switch s[i] {
			case '+':
				operator = Add
			case '-':
				operator = Sub
			case '*':
				operator = Mul
			case '/':
				operator = '/'
			case '<':
				if i < len(s)-1 && s[i+1] == '=' {
					operator = LessOrEqual
					i++
					break
				}
				operator = LessThan
			case '>':
				if i < len(s)-1 && s[i+1] == '=' {
					operator = GreaterOrEqual
					i++
					break
				}
				operator = GreaterThan
			case '!':
				i++
				if i >= len(s) {
					return makeInvalidOperatorMissingRightOperand(-1), false
				}
				if s[i] == '=' {
					operator = NotEqual
					break
				}

				eatInvalidOperator()

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case '=':
				i++
				if i >= len(s) {
					return makeInvalidOperatorMissingRightOperand(-1), false
				}
				if s[i] == '=' {
					operator = Equal
					break
				}

				eatInvalidOperator()

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case 'a':
				AND_LEN := len("and")

				if len(s)-i >= AND_LEN && string(s[i:i+AND_LEN]) == "and" {
					operator = And
					i += AND_LEN - 1
					break
				}

				eatInvalidOperator()

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case 'i':
				i++
				if i >= len(s) {
					return makeInvalidOperatorMissingRightOperand(-1), false
				}
				if s[i] == 'n' {
					operator = In
					break
				}

				//TODO: eat some chars

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case 'k':
				KEYOF_LEN := len("keyof")
				if len(s)-i >= KEYOF_LEN && string(s[i:i+KEYOF_LEN]) == "keyof" {
					operator = Keyof
					i += KEYOF_LEN - 1
					break
				}

				eatInvalidOperator()

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case 'n':
				NOTIN_LEN := len("not-in")
				if len(s)-i >= NOTIN_LEN && string(s[i:i+NOTIN_LEN]) == "not-in" {
					operator = NotIn
					i += NOTIN_LEN - 1
					break
				}

				NOTMATCH_LEN := len("not-match")
				if len(s)-i >= NOTMATCH_LEN && string(s[i:i+NOTMATCH_LEN]) == "not-match" {
					operator = NotMatch
					i += NOTMATCH_LEN - 1
					break
				}

				eatInvalidOperator()

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case 'm':
				MATCH_LEN := len("match")
				if len(s)-i >= MATCH_LEN && string(s[i:i+MATCH_LEN]) == "match" {
					operator = Match
					i += MATCH_LEN - 1
					break
				}

				eatInvalidOperator()

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case 'o':
				OR_LEN := len("or")
				if len(s)-i >= OR_LEN && string(s[i:i+OR_LEN]) == "or" {
					operator = Or
					i += OR_LEN - 1
					break
				}

				eatInvalidOperator()

				parsingErr = &ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			case 's':
				SUBSTROF_LEN := len("substrof")
				if len(s)-i >= SUBSTROF_LEN && string(s[i:i+SUBSTROF_LEN]) == "substrof" {
					operator = Substrof
					i += SUBSTROF_LEN - 1
					break
				}
				panic(ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			case '.':
				operator = Dot
			}

			i++

			if i < len(s)-1 && s[i] == '.' {
				switch operator {
				case Add, Sub, Mul, Div, GreaterThan, GreaterOrEqual, LessThan, LessOrEqual, Dot:
					i++
					operator++
				default:
					parsingErr = &ParsingError{
						"invalid binary expression, non existing operator",
						i,
						openingParenIndex,
						KnownType,
						(*BinaryExpression)(nil),
					}
				}
			}

			if operator == Range && i < len(s) && s[i] == '<' {
				operator = ExclEndRange
				i++
			}

			eatSpace()

			if i >= len(s) {
				parsingErr = &ParsingError{
					UNTERMINATED_BIN_EXPR + " missing right operand",
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			}

			right, isMissingExpr := parseExpression()

			eatSpace()
			if isMissingExpr {
				parsingErr = &ParsingError{
					INVALID_BIN_EXPR + " missing right operand",
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}

			} else if i >= len(s) {
				parsingErr = &ParsingError{
					UNTERMINATED_BIN_EXPR + " missing closing parenthesis",
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				}
			}

			if i < len(s) {
				if s[i] != ')' {
					parsingErr = &ParsingError{
						UNTERMINATED_BIN_EXPR + " missing closing parenthesis",
						i,
						openingParenIndex,
						KnownType,
						(*BinaryExpression)(nil),
					}
				} else {
					i++
				}
			}

			lhs = &BinaryExpression{
				NodeBase: NodeBase{
					Span: NodeSpan{openingParenIndex, i},
					Err:  parsingErr,
				},
				Operator: operator,
				Left:     left,
				Right:    right,
			}
			parsingErr = nil
		}

		first = lhs

		//member expressions, index/slice expressions, extraction expression
		if lhs != nil && i < len(s) && (s[i] == '[' || s[i] == '.') {
			i++

			for {
				start := i

				if i >= len(s) {
					return &InvalidMemberLike{
						NodeBase: NodeBase{
							NodeSpan{first.Base().Span.Start, i},
							&ParsingError{
								"unterminated member/index expression",
								i,
								first.Base().Span.Start,
								UnspecifiedCategory,
								nil,
							},
							nil,
						},
						Left: lhs,
					}, false
				}

				if s[i-1] == '[' { //index/slice expression
					eatSpace()

					if i >= len(s) {
						return &InvalidMemberLike{
							NodeBase: NodeBase{
								NodeSpan{first.Base().Span.Start, i},
								&ParsingError{
									"unterminated member/index expression",
									i,
									first.Base().Span.Start,
									UnspecifiedCategory,
									nil,
								},
								nil,
							},
							Left: lhs,
						}, false
					}

					var startIndex Node
					var endIndex Node
					isSliceExpr := s[i] == ':'

					if isSliceExpr {
						i++
					} else {
						startIndex, _ = parseExpression()
					}

					eatSpace()

					if i >= len(s) {
						return &InvalidMemberLike{
							NodeBase: NodeBase{
								NodeSpan{first.Base().Span.Start, i},
								&ParsingError{
									"unterminated index/slice expression",
									i,
									first.Base().Span.Start,
									UnspecifiedCategory,
									nil,
								},
								nil,
							},
							Left: lhs,
						}, false
					}

					if s[i] == ':' {
						if isSliceExpr {
							return &SliceExpression{
								NodeBase: NodeBase{
									NodeSpan{first.Base().Span.Start, i},
									&ParsingError{
										"invalid slice expression, a single colon should be present",
										i,
										first.Base().Span.Start,
										UnspecifiedCategory,
										nil,
									},
									nil,
								},
								Indexed:    lhs,
								StartIndex: startIndex,
								EndIndex:   endIndex,
							}, false
						}
						isSliceExpr = true
						i++
					}

					eatSpace()

					if isSliceExpr && startIndex == nil && (i >= len(s) || s[i] == ']') {
						return &SliceExpression{
							NodeBase: NodeBase{
								NodeSpan{first.Base().Span.Start, i},
								&ParsingError{
									"unterminated slice expression, missing end index",
									i,
									first.Base().Span.Start,
									UnspecifiedCategory,
									nil,
								},
								nil,
							},
							Indexed:    lhs,
							StartIndex: startIndex,
							EndIndex:   endIndex,
						}, false
					}

					if i < len(s) && s[i] != ']' && isSliceExpr {
						endIndex, _ = parseExpression()
					}

					eatSpace()

					if i >= len(s) || s[i] != ']' {
						return &InvalidMemberLike{
							NodeBase: NodeBase{
								NodeSpan{first.Base().Span.Start, i},
								&ParsingError{
									"unterminated index/slice expression, missing closing bracket ']'",
									i,
									first.Base().Span.Start,
									UnspecifiedCategory,
									nil,
								},
								nil,
							},
							Left: lhs,
						}, false
					}

					i++

					spanStart := lhs.Base().Span.Start
					if lhs == first {
						spanStart = parenthesizedFirstStart
					}

					if isSliceExpr {
						return &SliceExpression{
							NodeBase: NodeBase{
								NodeSpan{spanStart, i},
								nil,
								nil,
							},
							Indexed:    lhs,
							StartIndex: startIndex,
							EndIndex:   endIndex,
						}, false
					}

					lhs = &IndexExpression{
						NodeBase: NodeBase{
							NodeSpan{spanStart, i},
							nil,
							nil,
						},
						Indexed: lhs,
						Index:   startIndex,
					}
				} else if s[i] == '{' { //extraction expression (result is returned, the loop is not continued)
					i--
					keyList := parseKeyList()

					return &ExtractionExpression{
						NodeBase: NodeBase{
							NodeSpan{lhs.Base().Span.Start, keyList.Span.End},
							nil,
							nil,
						},
						Object: lhs,
						Keys:   keyList,
					}, false
				} else { //member expression
					if !isAlpha(s[i]) && s[i] != '_' {
						return &MemberExpression{
							NodeBase: NodeBase{
								NodeSpan{lhs.Base().Span.Start, i},
								&ParsingError{
									"property name should start with a letter not '" + string(s[i]) + "'",
									i,
									first.Base().Span.Start,
									KnownType,
									(*MemberExpression)(nil),
								},
								nil,
							},
							Left:         lhs,
							PropertyName: nil,
						}, false
					}

					for i < len(s) && isIdentChar(s[i]) {
						i++
					}

					propName := string(s[start:i])
					spanStart := lhs.Base().Span.Start
					if lhs == first {
						spanStart = parenthesizedFirstStart
					}

					lhs = &MemberExpression{
						NodeBase: NodeBase{
							NodeSpan{spanStart, i},
							nil,
							nil,
						},
						Left: lhs,
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{start, i},
								nil,
								nil,
							},
							Name: propName,
						},
					}

				}
				if i >= len(s) || (s[i] != '.' && s[i] != '[') || s[i+1] == '(' {
					break
				}
				i++
			}
		}

		//call: <lhs> '(' ...
		if lhs != nil && i < len(s) && s[i] == '(' {

			i++
			spanStart := lhs.Base().Span.Start

			if lhs == first {
				spanStart = parenthesizedFirstStart
			}

			call := &Call{
				NodeBase: NodeBase{
					NodeSpan{spanStart, 0},
					nil,
					nil,
				},
				Callee:    lhs,
				Arguments: nil,
			}

			//parse arguments
			for i < len(s) && s[i] != ')' {
				eatSpaceNewlineComma()

				if i >= len(s) || s[i] == ')' {
					break
				}

				arg, _ := parseExpression()

				call.Arguments = append(call.Arguments, arg)
				eatSpaceNewlineComma()
			}

			var parsingErr *ParsingError

			if i >= len(s) || s[i] != ')' {
				parsingErr = &ParsingError{
					"unterminated call, missing closing parenthesis ')'",
					i,
					first.Base().Span.Start,
					KnownType,
					(*Call)(nil),
				}
			} else {
				i++
			}

			if i < len(s) && s[i] == '!' {
				call.Must = true
				i++
			}

			call.NodeBase.Span.End = i
			call.Err = parsingErr
			return call, false
		}

		if lhs != nil {
			return lhs, false
		}

		left := string(s[max(0, i-5):i])
		right := string(s[i:min(len(s), i+5)])

		return &MissingExpression{
			NodeBase: NodeBase{
				Span: NodeSpan{i - 1, i},
				Err: &ParsingError{
					fmt.Sprintf("an expression was expected: ...%s<<here>>%s...", left, right),
					i,
					i - 1,
					UnspecifiedCategory,
					nil,
				},
			},
		}, true
	}

	parseRequirements = func() *Requirements {
		var requirements *Requirements
		if i < len(s) && strings.HasPrefix(string(s[i:]), REQUIRE_KEYWORD_STR) {
			tokens := []ValuelessToken{{REQUIRE_KEYWORD, NodeSpan{i, i + len(REQUIRE_KEYWORD_STR)}}}
			i += len(REQUIRE_KEYWORD_STR)

			eatSpace()
			requirementObject, _ := parseExpression()
			requirements = &Requirements{
				ValuelessTokens: tokens,
				Object:          requirementObject.(*ObjectLiteral),
			}

		}
		return requirements
	}

	parseGlobalConstantDeclarations = func() *GlobalConstantDeclarations {
		start := i
		constKeywordSpan := NodeSpan{i, i + len(CONST_KEYWORD_STR)}

		if i < len(s) && strings.HasPrefix(string(s[i:]), CONST_KEYWORD_STR) {
			i += len(CONST_KEYWORD_STR)

			eatSpace()
			var declarations []*GlobalConstantDeclaration
			var parsingErr *ParsingError

			if i >= len(s) {
				return &GlobalConstantDeclarations{
					NodeBase: NodeBase{
						NodeSpan{start, i},
						&ParsingError{
							"unterminated global const declarations",
							i,
							start,
							KnownType,
							(*GlobalConstantDeclarations)(nil),
						},
						[]ValuelessToken{{CONST_KEYWORD, constKeywordSpan}},
					},
				}
			}

			if s[i] != '(' {
				parsingErr = &ParsingError{
					"invalid global const declarations, expected opening parenthesis after 'const'",
					i,
					start,
					KnownType,
					(*GlobalConstantDeclarations)(nil),
				}
			}

			i++

			for i < len(s) && s[i] != ')' {
				var declParsingErr *ParsingError
				eatSpaceAndNewLineAndComment()

				if i < len(s) && s[i] == ')' {
					break
				}

				if i >= len(s) {
					parsingErr = &ParsingError{
						"invalid global const declarations, missing closing parenthesis",
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					}
					break
				}

				lhs, _ := parseExpression()
				globvar, ok := lhs.(*IdentifierLiteral)
				if !ok {
					declParsingErr = &ParsingError{
						"invalid global const declaration, left hand sides must be an identifier",
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					}
				}

				eatSpace()

				if i >= len(s) || s[i] != '=' {
					declParsingErr = &ParsingError{
						fmt.Sprintf("invalid global const declaration, missing '=' after name %s", globvar.Name),
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					}

					if i < len(s) {
						i++
					}
					declarations = append(declarations, &GlobalConstantDeclaration{
						NodeBase: NodeBase{
							NodeSpan{lhs.Base().Span.Start, i},
							declParsingErr,
							nil,
						},
						Left: lhs.(*IdentifierLiteral),
					})
					break
				}

				i++
				eatSpace()

				var rhs Node

				if i >= len(s) || s[i] == ')' {
					declParsingErr = &ParsingError{
						fmt.Sprintf("invalid global const declarations, missing value after '%s ='", globvar.Name),
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					}
				} else {
					rhs, _ = parseExpression()
					if !IsSimpleValueLiteral(rhs) {
						declParsingErr = &ParsingError{
							fmt.Sprintf("invalid global const declarations, only literals are allowed as values : %T", rhs),
							i,
							start,
							KnownType,
							(*GlobalConstantDeclarations)(nil),
						}
					}
				}

				declarations = append(declarations, &GlobalConstantDeclaration{
					NodeBase: NodeBase{
						NodeSpan{lhs.Base().Span.Start, rhs.Base().Span.End},
						declParsingErr,
						nil,
					},
					Left:  lhs.(*IdentifierLiteral),
					Right: rhs,
				})

				eatSpaceAndNewLineAndComment()
			}

			i++

			decls := &GlobalConstantDeclarations{
				NodeBase: NodeBase{
					NodeSpan{start, i},
					parsingErr,
					[]ValuelessToken{{CONST_KEYWORD, constKeywordSpan}},
				},
				Declarations: declarations,
			}

			return decls
		}

		return nil
	}

	parseCallArgs := func(call *Call) {
		for i < len(s) && s[i] != '\n' && !isNotPairedOrIsClosingDelim(s[i]) {
			eatSpaceAndComments()

			if s[i] == '\n' || isNotPairedOrIsClosingDelim(s[i]) {
				break
			}

			arg, _ := parseExpression()

			call.Arguments = append(call.Arguments, arg)
			eatSpaceAndComments()
		}
	}

	parseFunction = func(start int) Node {
		tokens := []ValuelessToken{{FN_KEYWORD, NodeSpan{i - 2, i}}}
		eatSpace()

		var ident *IdentifierLiteral
		var parsingErr *ParsingError

		if i < len(s) && isAlpha(s[i]) {
			idnt := parseIdentLike()
			var ok bool
			if ident, ok = idnt.(*IdentifierLiteral); !ok {
				return &FunctionDeclaration{
					NodeBase: NodeBase{
						Span: NodeSpan{start, i},
						Err: &ParsingError{
							fmt.Sprintf("function name should be an identifier not a(n) %T", idnt),
							i,
							start,
							KnownType,
							(*FunctionDeclaration)(nil),
						},
						ValuelessTokens: tokens,
					},
					Function: nil,
					Name:     nil,
				}
			}
		}

		if i >= len(s) || s[i] != '(' {
			parsingErr = &ParsingError{
				"function : fn keyword (or function name) should be followed by '(' <param list> ')' ",
				i,
				start,
				UnspecifiedCategory,
				nil,
			}

			fn := FunctionExpression{
				NodeBase: NodeBase{
					Span: NodeSpan{start, i},
				},
			}

			if ident != nil {
				return &FunctionDeclaration{
					NodeBase: NodeBase{
						Span:            fn.Span,
						Err:             parsingErr,
						ValuelessTokens: tokens,
					},
					Function: &fn,
					Name:     ident,
				}
			}
		}

		i++

		var parameters []*FunctionParameter

		for i < len(s) && s[i] != ')' {
			eatSpaceNewlineComma()

			if i < len(s) && s[i] == ')' {
				break
			}

			varNode, _ := parseExpression()

			if _, ok := varNode.(*IdentifierLiteral); !ok {
				parameters = append(parameters, &FunctionParameter{
					NodeBase: NodeBase{
						varNode.Base().Span,
						&ParsingError{
							"function : the parameter list should contain variables separated by a comma",
							i,
							start,
							UnspecifiedCategory,
							nil,
						},
						nil,
					},
					Var: nil,
				})
			} else {
				parameters = append(parameters, &FunctionParameter{
					NodeBase: NodeBase{
						varNode.Base().Span,
						nil,
						nil,
					},
					Var: varNode.(*IdentifierLiteral),
				})
			}

			eatSpaceNewlineComma()
		}

		var requirements *Requirements
		var blk *Block

		if i >= len(s) {
			parsingErr = &ParsingError{
				"function : unterminated parameter list : missing closing parenthesis",
				i,
				start,
				UnspecifiedCategory,
				nil,
			}
		} else if s[i] != ')' {
			parsingErr = &ParsingError{
				"function : invalid syntax",
				i,
				start,
				UnspecifiedCategory,
				nil,
			}
		} else {
			i++

			eatSpace()

			requirements = parseRequirements()

			eatSpace()
			if i >= len(s) || s[i] != '{' {
				return &FunctionExpression{
					NodeBase: NodeBase{
						Span: NodeSpan{start, i},
						Err: &ParsingError{
							"function : parameter list should be followed by a block, not " + string(s[i]),
							i,
							start,
							UnspecifiedCategory,
							nil,
						},
						ValuelessTokens: tokens,
					},
					Parameters:   parameters,
					Body:         blk,
					Requirements: requirements,
				}

			}

			blk = parseBlock()
		}

		fn := FunctionExpression{
			NodeBase: NodeBase{
				Span:            NodeSpan{start, blk.Span.End},
				Err:             parsingErr,
				ValuelessTokens: tokens,
			},
			Parameters:   parameters,
			Body:         blk,
			Requirements: requirements,
		}

		if ident != nil {
			fn.Err = nil
			return &FunctionDeclaration{
				NodeBase: NodeBase{
					Span:            fn.Span,
					Err:             parsingErr,
					ValuelessTokens: tokens,
				},
				Function: &fn,
				Name:     ident,
			}
		}

		return &fn
	}

	parseStatement = func() Statement {
		expr, _ := parseExpression()

		var b rune
		followedBySpace := false
		isAKeyword := false

		switch expr.(type) {
		case *IdentifierLiteral, *IdentifierMemberExpression: //funcname <no args>

			if idnt, isIdentLiteral := expr.(*IdentifierLiteral); isIdentLiteral && isKeyword(idnt.Name) {
				isAKeyword = isKeyword(idnt.Name)
				break
			}

			prevI := i
			eatSpace()

			if i >= len(s) || s[i] == '\n' || s[i] == ';' {
				if i < len(s) {
					i++
				}
				return &Call{
					NodeBase: NodeBase{
						Span: NodeSpan{expr.Base().Span.Start, i},
					},
					Callee:    expr,
					Arguments: nil,
					Must:      true,
				}
			} else {
				i = prevI
			}
		}

		if i >= len(s) {
			if !isAKeyword {
				return expr
			}
		} else {
			b = s[i]
			followedBySpace = b == ' '
		}

		switch ev := expr.(type) {
		case *Call:
			return ev
		case *IdentifierLiteral:
			switch ev.Name {
			case "if":
				var alternate *Block
				var blk *Block
				var end int
				var parsingErr *ParsingError

				tokens := []ValuelessToken{
					{Type: IF_KEYWORD, Span: ev.Span},
				}

				eatSpace()
				test, _ := parseExpression()
				eatSpace()

				if i >= len(s) {
					parsingErr = &ParsingError{
						"unterminated if statement, missing block",
						i,
						expr.Base().Span.Start,
						KnownType,
						(*IfStatement)(nil),
					}
				} else if s[i] != '{' {
					parsingErr = &ParsingError{
						"invalid if statement, test expression should be followed by a block, not " + string(s[i]),
						i,
						expr.Base().Span.Start,
						KnownType,
						(*IfStatement)(nil),
					}
				} else {
					blk = parseBlock()
					end = blk.Span.End
					eatSpace()

					if i < len(s)-4 && string(s[i:i+4]) == "else" {
						tokens = append(tokens, ValuelessToken{
							Type: ELSE_KEYWORD,
							Span: NodeSpan{i, i + 4},
						})
						i += 4
						eatSpace()

						if i >= len(s) {
							parsingErr = &ParsingError{
								"unterminated if statement, missing block after 'else'",
								i,
								expr.Base().Span.Start,
								KnownType,
								(*IfStatement)(nil),
							}
						} else if s[i] != '{' {
							parsingErr = &ParsingError{
								"invalid if statement, else should be followed by a block, not " + string(s[i]),
								i,
								expr.Base().Span.Start,
								KnownType,
								(*IfStatement)(nil),
							}
						} else {
							alternate = parseBlock()
							end = alternate.Span.End
						}
					}
				}

				return &IfStatement{
					NodeBase: NodeBase{
						Span:            NodeSpan{ev.Span.Start, end},
						Err:             parsingErr,
						ValuelessTokens: tokens,
					},
					Test:       test,
					Consequent: blk,
					Alternate:  alternate,
				}
			case "for":
				var parsingErr *ParsingError
				forStart := expr.Base().Span.Start
				eatSpace()
				keyIndexIdent, _ := parseExpression()

				tokens := []ValuelessToken{{FOR_KEYWORD, ev.Span}}

				switch v := keyIndexIdent.(type) {
				case *IdentifierLiteral:
					eatSpace()

					if i > len(s) {
						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"invalid for statement",
									i,
									forStart,
									KnownType,
									(*ForStatement)(nil),
								},
							},
						}
					}

					if s[i] != ',' {
						parsingErr = &ParsingError{
							"for statement : key/index name should be followed by a comma ',' , not " + string(s[i]),
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						}
					}

					tokens = append(tokens, ValuelessToken{COMMA, NodeSpan{i, i + 1}})

					i++
					eatSpace()

					if i > len(s) {
						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"unterminated for statement",
									i,
									forStart,
									KnownType,
									(*ForStatement)(nil),
								},
							},
						}
					}

					valueElemIdent, _ := parseExpression()

					if _, isVar := valueElemIdent.(*IdentifierLiteral); !isVar {
						parsingErr = &ParsingError{
							fmt.Sprintf("invalid for statement : 'for <key-index var> <colon> should be followed by a variable, not a(n) %T", keyIndexIdent),
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						}
					}

					eatSpace()

					if i >= len(s) {
						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"unterminated for statement",
									i,
									forStart,
									KnownType,
									(*ForStatement)(nil),
								},
							},
						}
					}

					if s[i] != 'i' || i > len(s)-2 || s[i+1] != 'n' {
						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"invalid for statement : missing 'in' keyword ",
									i,
									forStart,
									KnownType,
									(*ForStatement)(nil),
								},
							},
							KeyIndexIdent:  keyIndexIdent.(*IdentifierLiteral),
							ValueElemIdent: valueElemIdent.(*IdentifierLiteral),
						}
					}

					tokens = append(tokens, ValuelessToken{IN_KEYWORD, NodeSpan{i, i + 2}})
					i += 2

					if i < len(s) && s[i] != ' ' {
						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"invalid for statement : 'in' keyword should be followed by a space",
									i,
									forStart,
									KnownType,
									(*ForStatement)(nil),
								},
							},
							KeyIndexIdent:  keyIndexIdent.(*IdentifierLiteral),
							ValueElemIdent: valueElemIdent.(*IdentifierLiteral),
						}
					}
					eatSpace()

					if i >= len(s) {
						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"unterminated for statement, missing value after 'in'",
									i,
									forStart,
									KnownType,
									(*ForStatement)(nil),
								},
							},
							KeyIndexIdent:  keyIndexIdent.(*IdentifierLiteral),
							ValueElemIdent: valueElemIdent.(*IdentifierLiteral),
						}
					}

					iteratedValue, _ := parseExpression()
					eatSpace()
					var blk *Block

					if i >= len(s) {
						parsingErr = &ParsingError{
							"unterminated for statement, missing block",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						}
					} else {
						blk = parseBlock()
					}

					return &ForStatement{
						NodeBase: NodeBase{
							Span:            NodeSpan{ev.Span.Start, blk.Span.End},
							Err:             parsingErr,
							ValuelessTokens: tokens,
						},
						KeyIndexIdent:  keyIndexIdent.(*IdentifierLiteral),
						ValueElemIdent: valueElemIdent.(*IdentifierLiteral),
						Body:           blk,
						IteratedValue:  iteratedValue,
					}
				case *BinaryExpression:
					if v.Operator == Range || v.Operator == ExclEndRange {
						iteratedValue := keyIndexIdent
						keyIndexIdent = nil

						eatSpace()
						var blk *Block

						if i >= len(s) {
							parsingErr = &ParsingError{
								"unterminated for statement, missing block",
								i,
								forStart,
								KnownType,
								(*ForStatement)(nil),
							}
						} else {
							blk = parseBlock()
						}

						return &ForStatement{
							NodeBase: NodeBase{
								Span:            NodeSpan{ev.Span.Start, blk.Span.End},
								Err:             parsingErr,
								ValuelessTokens: tokens,
							},
							KeyIndexIdent:  nil,
							ValueElemIdent: nil,
							Body:           blk,
							IteratedValue:  iteratedValue,
						}
					}
					return &ForStatement{
						NodeBase: NodeBase{
							Span: NodeSpan{ev.Span.Start, i},
							Err: &ParsingError{
								fmt.Sprintf("invalid for statement : 'for' should be followed by a binary range expression, operator is %s", v.Operator.String()),
								i,
								forStart,
								KnownType,
								(*ForStatement)(nil),
							},
						},
					}

				default:
					return &ForStatement{
						NodeBase: NodeBase{
							Span: NodeSpan{ev.Span.Start, i},
							Err: &ParsingError{
								fmt.Sprintf("invalid for statement : 'for' should be followed by a variable or a binary range expression (binary range operator), not a(n) %T", keyIndexIdent),
								i,
								forStart,
								KnownType,
								(*ForStatement)(nil),
							},
						},
					}
				}

			case "switch", "match":
				switchMatchStart := expr.Base().Span.Start
				var tokens []ValuelessToken
				if ev.Name[0] == 's' {
					tokens = append(tokens, ValuelessToken{SWITCH_KEYWORD, expr.Base().Span})
				} else {
					tokens = append(tokens, ValuelessToken{MATCH_KEYWORD, expr.Base().Span})
				}

				eatSpace()

				if i >= len(s) {

					if ev.Name == "switch" {
						return &SwitchStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"unterminated switch statement : missing value",
									i,
									switchMatchStart,
									KnownType,
									(*SwitchStatement)(nil),
								},
								ValuelessTokens: tokens,
							},
						}
					}

					return &SwitchStatement{
						NodeBase: NodeBase{
							Span: NodeSpan{ev.Span.Start, i},
							Err: &ParsingError{
								"unterminated match statement : missing value",
								i,
								switchMatchStart,
								KnownType,
								(*SwitchStatement)(nil),
							},
							ValuelessTokens: tokens,
						},
					}
				}

				discriminant, _ := parseExpression()
				var switchCases []*Case

				eatSpace()

				if i >= len(s) || s[i] != '{' {
					if ev.Name == "switch" {
						return &SwitchStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"unterminated switch statement : missing body",
									i,
									switchMatchStart,
									KnownType,
									(*SwitchStatement)(nil),
								},
								ValuelessTokens: tokens,
							},
							Discriminant: discriminant,
						}
					}

					return &MatchStatement{
						NodeBase: NodeBase{
							Span: NodeSpan{ev.Span.Start, i},
							Err: &ParsingError{
								"unterminated match statement : missing body",
								i,
								switchMatchStart,
								KnownType,
								(*SwitchStatement)(nil),
							},
							ValuelessTokens: tokens,
						},
						Discriminant: discriminant,
					}
				}

				i++

				for i < len(s) && s[i] != '}' {
					eatSpaceNewLineSemiColonComment()

					if i < len(s) && s[i] == '}' {
						break
					}

					var valueNodes []Node
					var caseParsingErr *ParsingError

					//parse gathered cases
					for i < len(s) && s[i] != '{' {
						if i >= len(s) {
							if ev.Name == "switch" {
								return &SwitchStatement{
									NodeBase: NodeBase{
										Span: NodeSpan{ev.Span.Start, i},
										Err: &ParsingError{
											"unterminated switch statement",
											i,
											switchMatchStart,
											KnownType,
											(*SwitchStatement)(nil),
										},
										ValuelessTokens: tokens,
									},
									Discriminant: discriminant,
								}
							}

							return &MatchStatement{
								NodeBase: NodeBase{
									Span: NodeSpan{ev.Span.Start, i},
									Err: &ParsingError{
										"unterminated match statement",
										i,
										switchMatchStart,
										KnownType,
										(*SwitchStatement)(nil),
									},
									ValuelessTokens: tokens,
								},
								Discriminant: discriminant,
							}

						}
						valueNode, _ := parseExpression()

						if !IsSimpleValueLiteral(valueNode) {
							if ev.Name == "switch" {
								caseParsingErr = &ParsingError{
									"invalid switch case : only simple value literals are supported (1, 1.0, /home, ..)",
									i,
									switchMatchStart,
									KnownType,
									(*SwitchStatement)(nil),
								}
							} else {
								caseParsingErr = &ParsingError{
									"invalid match case : only simple value literals are supported (1, 1.0, /home, ..)",
									i,
									switchMatchStart,
									KnownType,
									(*MatchStatement)(nil),
								}
							}
						}
						valueNodes = append(valueNodes, valueNode)

						eatSpace()

						if i < len(s) && s[i] == ',' {
							i++
						} else {
							break
						}

						eatSpace()
					}

					if i >= len(s) || s[i] != '{' {
						if ev.Name == "switch" {
							caseParsingErr = &ParsingError{
								"invalid switch case : missing block",
								i,
								switchMatchStart,
								KnownType,
								(*SwitchStatement)(nil),
							}
						} else {

							caseParsingErr = &ParsingError{
								"invalid match case : missing block",
								i,
								switchMatchStart,
								KnownType,
								(*MatchStatement)(nil),
							}
						}
					}

					blk := parseBlock()

					for _, valNode := range valueNodes {
						switchCase := &Case{
							NodeBase: NodeBase{
								NodeSpan{valNode.Base().Span.Start, blk.Span.End},
								caseParsingErr,
								nil,
							},
							Value: valNode,
							Block: blk,
						}

						switchCases = append(switchCases, switchCase)
					}

					eatSpaceNewLineSemiColonComment()
				}

				var parsingErr *ParsingError

				if i >= len(s) || s[i] != '}' {
					if ev.Name == "switch" {
						parsingErr = &ParsingError{
							"unterminated switch statement : missing closing body brace '}'",
							i,
							switchMatchStart,
							KnownType,
							(*SwitchStatement)(nil),
						}
					} else {
						parsingErr = &ParsingError{
							"unterminated match statement : missing closing body brace '}'",
							i,
							switchMatchStart,
							KnownType,
							(*MatchStatement)(nil),
						}
					}

				}

				i++

				if ev.Name == "switch" {

					return &SwitchStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
							parsingErr,
							tokens,
						},
						Discriminant: discriminant,
						Cases:        switchCases,
					}
				}

				return &MatchStatement{
					NodeBase: NodeBase{
						NodeSpan{ev.Span.Start, i},
						parsingErr,
						tokens,
					},
					Discriminant: discriminant,
					Cases:        switchCases,
				}

			case "fn":
				fn := parseFunction(ev.Span.Start)

				return fn
			case "drop-perms":
				eatSpace()

				e, _ := parseExpression()
				objLit, ok := e.(*ObjectLiteral)

				var parsingErr *ParsingError
				if !ok {
					parsingErr = &ParsingError{
						"permission dropping statement: 'drop-perms' keyword should be followed by an object literal (permissions)",
						i,
						expr.Base().Span.Start,
						KnownType,
						(*ImportStatement)(nil),
					}
				}

				return &PermissionDroppingStatement{
					NodeBase: NodeBase{
						NodeSpan{expr.Base().Span.Start, objLit.Span.End},
						parsingErr,
						[]ValuelessToken{{DROP_PERMS_KEYWORD, ev.Span}},
					},
					Object: objLit,
				}

			case "import":
				importStart := expr.Base().Span.Start
				tokens := []ValuelessToken{
					{IMPORT_KEYWORD, ev.Span},
				}

				eatSpace()

				identifier := parseIdentLike()
				if _, ok := identifier.(*IdentifierLiteral); !ok {
					return &ImportStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
							&ParsingError{
								"import statement: import should be followed by an identifier",
								i,
								importStart,
								KnownType,
								(*ImportStatement)(nil),
							},
							tokens,
						},
					}

				}

				eatSpace()

				url_, _ := parseExpression()

				if _, ok := url_.(*URLLiteral); !ok {
					return &ImportStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
							&ParsingError{
								"import statement: URL should be a URL literal",
								i,
								importStart,
								KnownType,
								(*ImportStatement)(nil),
							},
							nil,
						},
					}
				}

				eatSpace()

				checksum, _ := parseExpression()
				if _, ok := checksum.(*StringLiteral); !ok {
					return &ImportStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
							&ParsingError{
								"import statement: checksum should be a string literal",
								i,
								importStart,
								KnownType,
								(*ImportStatement)(nil),
							},
							nil,
						},
						URL: url_.(*URLLiteral),
					}
				}

				eatSpace()

				argumentObject, _ := parseExpression()
				if _, ok := argumentObject.(*ObjectLiteral); !ok {
					return &ImportStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
							&ParsingError{
								"import statement: argument should be an object literal",
								i,
								importStart,
								KnownType,
								(*ImportStatement)(nil),
							},
							nil,
						},
						URL: url_.(*URLLiteral),
					}
				}

				eatSpace()
				allowIdent, _ := parseExpression()
				if ident, ok := allowIdent.(*IdentifierLiteral); !ok || ident.Name != "allow" {
					return &ImportStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
							&ParsingError{
								"import statement: argument should be followed by a the 'allow' keyword",
								i,
								importStart,
								KnownType,
								(*ImportStatement)(nil),
							},
							tokens,
						},
						URL:            url_.(*URLLiteral),
						ArgumentObject: argumentObject.(*ObjectLiteral),
					}
				}
				tokens = append(tokens, ValuelessToken{ALLOW_KEYWORD, allowIdent.Base().Span})

				eatSpace()
				grantedPerms, _ := parseExpression()
				grantedPermsLit, ok := grantedPerms.(*ObjectLiteral)
				if !ok {
					return &ImportStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
							&ParsingError{
								"import statement: 'allow' keyword should be followed by an object literal (permissions)",
								i,
								importStart,
								KnownType,
								(*ImportStatement)(nil),
							},
							tokens,
						},
						URL:            url_.(*URLLiteral),
						ArgumentObject: argumentObject.(*ObjectLiteral),
					}
				}

				return &ImportStatement{
					NodeBase: NodeBase{
						NodeSpan{ev.Span.Start, i},
						nil,
						tokens,
					},
					Identifier:         identifier.(*IdentifierLiteral),
					URL:                url_.(*URLLiteral),
					ValidationString:   checksum.(*StringLiteral),
					ArgumentObject:     argumentObject.(*ObjectLiteral),
					GrantedPermissions: grantedPermsLit,
				}

			case "return":
				var end int = i
				var returnValue Node

				eatSpace()

				if i < len(s) && s[i] != ';' && s[i] != '}' && s[i] != '\n' {
					returnValue, _ = parseExpression()
					end = returnValue.Base().Span.End
				}

				return &ReturnStatement{
					NodeBase: NodeBase{
						Span:            NodeSpan{ev.Span.Start, end},
						ValuelessTokens: []ValuelessToken{{RETURN_KEYWORD, ev.Span}},
					},
					Expr: returnValue,
				}
			case "break":
				return &BreakStatement{
					NodeBase: NodeBase{
						Span:            ev.Span,
						ValuelessTokens: []ValuelessToken{{BREAK_KEYWORD, ev.Span}},
					},
					Label: nil,
				}
			case "continue":
				return &ContinueStatement{
					NodeBase: NodeBase{
						Span:            ev.Span,
						ValuelessTokens: []ValuelessToken{{CONTINUE_KEYWORD, ev.Span}},
					},
					Label: nil,
				}
			case "assign":
				var vars []Node

				for i < len(s) && s[i] != '=' {
					eatSpace()
					e, _ := parseExpression()
					if _, ok := e.(*IdentifierLiteral); !ok {
						return &MultiAssignment{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, i},
								Err: &ParsingError{
									"assign keyword should be followed by identifiers (assign a b = <value>)",
									i,
									expr.Base().Span.Start,
									KnownType,
									(*MultiAssignment)(nil),
								},
							},
							Variables: vars,
						}
					}
					vars = append(vars, e)
					eatSpace()

				}

				var right Node
				var parsingErr *ParsingError

				if i >= len(s) {
					parsingErr = &ParsingError{
						"unterminated multi assign statement, missing '='",
						i,
						expr.Base().Span.Start,
						KnownType,
						(*MultiAssignment)(nil),
					}
				} else {
					i++
					eatSpace()
					right, _ = parseExpression()
				}

				return &MultiAssignment{
					NodeBase: NodeBase{
						Span: NodeSpan{ev.Span.Start, right.Base().Span.End},
						Err:  parsingErr,
						ValuelessTokens: []ValuelessToken{
							{ASSIGN_KEYWORD, ev.Span},
						},
					},
					Variables: vars,
					Right:     right,
				}
			}

		}

		eatSpace()

		if i >= len(s) {
			return expr
		}

		switch s[i] {
		case '=':
			i++
			eatSpace()

			if i >= len(s) {
				return &Assignment{
					NodeBase: NodeBase{
						Span: NodeSpan{expr.Base().Span.Start, i},
						Err: &ParsingError{
							"unterminated assignment, missing value after '='",
							i,
							expr.Base().Span.Start,
							KnownType,
							(*Assignment)(nil),
						},
					},
					Left: expr,
				}
			}

			var right Node

			if s[i] == '|' {
				i++
				eatSpace()
				right = parseStatement()
				pipeline, ok := right.(*PipelineStatement)

				if !ok {
					return &Assignment{
						NodeBase: NodeBase{
							Span: NodeSpan{expr.Base().Span.Start, i},
							Err: &ParsingError{
								"invalid assignment, a pipeline expression was expected after '|'",
								i,
								expr.Base().Span.Start,
								KnownType,
								(*Assignment)(nil),
							},
						},
						Left:  expr,
						Right: right,
					}
				}

				right = &PipelineExpression{
					NodeBase: pipeline.NodeBase,
					Stages:   pipeline.Stages,
				}
			} else {
				right, _ = parseExpression()
			}

			return &Assignment{
				NodeBase: NodeBase{
					Span: NodeSpan{expr.Base().Span.Start, right.Base().Span.End},
				},
				Left:  expr,
				Right: right,
			}
		case ';':
			return expr
		default:

			switch expr.(type) {
			case *IdentifierLiteral, *IdentifierMemberExpression: //funcname args...

				if (!followedBySpace && s[i] != '\n') || (isNotPairedOrIsClosingDelim(s[i]) && s[i] != '(' && s[i] != '|' && s[i] != '\n') {
					break
				}

				call := &Call{
					NodeBase: NodeBase{
						Span: NodeSpan{expr.Base().Span.Start, 0},
					},
					Callee:    expr,
					Arguments: nil,
					Must:      true,
				}

				parseCallArgs(call)

				if i < len(s) && s[i] == '\n' {
					i++
				}

				if len(call.Arguments) == 0 {
					call.NodeBase.Span.End = expr.Base().Span.End
				} else {
					call.NodeBase.Span.End = call.Arguments[len(call.Arguments)-1].Base().Span.End
				}

				eatSpace()

				//normal call

				if i >= len(s) || s[i] != '|' {
					return call
				}

				//pipe statement

				stmt := &PipelineStatement{
					NodeBase: NodeBase{
						NodeSpan{call.Span.Start, 0},
						nil,
						nil,
					},
					Stages: []*PipelineStage{
						{
							Kind: NormalStage,
							Expr: call,
						},
					},
				}

				i++
				eatSpace()

				if i >= len(s) {
					stmt.Err = &ParsingError{
						"unterminated pipeline statement, last stage is empty",
						i,
						expr.Base().Span.Start,
						UnspecifiedCategory,
						nil,
					}
					return stmt
				}

				for i < len(s) && s[i] != '\n' {
					eatSpace()
					if i >= len(s) {
						stmt.Err = &ParsingError{
							"unterminated pipeline statement, last stage is empty",
							i,
							expr.Base().Span.Start,
							UnspecifiedCategory,
							nil,
						}
						return stmt
					}

					callee, _ := parseExpression()

					currentCall := &Call{
						NodeBase: NodeBase{
							Span: NodeSpan{callee.Base().Span.Start, 0},
						},
						Callee:    callee,
						Arguments: nil,
						Must:      true,
					}

					stmt.Stages = append(stmt.Stages, &PipelineStage{
						Kind: NormalStage,
						Expr: currentCall,
					})

					switch callee.(type) {
					case *IdentifierLiteral, *IdentifierMemberExpression:

						parseCallArgs(currentCall)

						if len(currentCall.Arguments) == 0 {
							currentCall.NodeBase.Span.End = callee.Base().Span.End
						} else {
							currentCall.NodeBase.Span.End = currentCall.Arguments[len(currentCall.Arguments)-1].Base().Span.End
						}

						stmt.Span.End = currentCall.Span.End

						eatSpace()

						if i >= len(s) {
							return stmt
						}

						switch s[i] {
						case '|':
							i++
							continue //we parse the next stage
						case '\n':
							i++
							return stmt
						case ';':
							i++
							return stmt
						default:
							stmt.Err = &ParsingError{
								fmt.Sprintf("invalid pipeline stage, unexpected char '%c'", s[i]),
								i,
								expr.Base().Span.Start,
								UnspecifiedCategory,
								nil,
							}
							return stmt
						}
					default:
						stmt.Err = &ParsingError{
							"invalid pipeline stage, all pipeline stages should be calls",
							i,
							expr.Base().Span.Start,
							UnspecifiedCategory,
							nil,
						}
						return stmt
					}
				}
			}
		}
		return expr
	}

	//end of closures

	var stmts []Node

	eatSpaceNewLineSemiColonComment()
	globalConstDecls := parseGlobalConstantDeclarations()

	eatSpaceNewLineSemiColonComment()
	requirements := parseRequirements()

	eatSpaceNewLineSemiColonComment()

	for i < len(s) {
		stmt := parseStatement()
		if _, isMissingExpr := stmt.(*MissingExpression); isMissingExpr {
			if isMissingExpr {
				i++

				if i >= len(s) {
					stmts = append(stmts, stmt)
					break
				}
			}
		}
		stmts = append(stmts, stmt)
		eatSpaceNewLineSemiColonComment()
	}

	mod.Requirements = requirements
	mod.Statements = stmts
	mod.GlobalConstantDeclarations = globalConstDecls

	return mod, nil
}

func IsSimpleGopherVal(v interface{}) bool {
	switch v.(type) {
	case rune, string, JSONstring, bool, int, float64,
		Identifier, Path, PathPattern, URL, HTTPHost, HTTPHostPattern, URLPattern:
		return true
	default:
		return false
	}
}

func IsGopherVal(v interface{}) bool {
	switch v.(type) {
	case rune, string, JSONstring, bool, int, float64, Object, List, Func, ExternalValue, Option,
		Identifier, Path, PathPattern, URL, HTTPHost, HTTPHostPattern, URLPattern:
		return true
	default:
		return false
	}
}

func ExtValOf(v interface{}, state *State) interface{} {
	v = ValOf(v)
	if IsSimpleGopherVal(v) {
		return v
	}
	if extVal, ok := v.(ExternalValue); ok {
		if extVal.state == state {
			return extVal.value
		}
		return extVal
	}
	return ExternalValue{
		state: state,
		value: v,
	}
}

//Unwraps any reflect.Value that wraps a Gopherscript value.
//Wraps its argument in a reflect.Value if it is not a Gopherscript value.
func ValOf(v interface{}) interface{} {
	if IsGopherVal(v) {
		return v
	}
	switch val := v.(type) {
	case reflect.Value:
		if !val.IsValid() {
			return val //return another value ?
		}
		intf := val.Interface()
		if IsGopherVal(intf) {
			return intf
		}
		return reflect.ValueOf(intf)
	default:
		return reflect.ValueOf(v)
	}
}

//Wraps its argument in a reflect.Value if it is not already wrapped.
func ToReflectVal(v interface{}) reflect.Value {
	switch val := v.(type) {
	case reflect.Value:
		return val
	default:
		return reflect.ValueOf(v)
	}
}

//Unwraps the content of a reflect.Value.
func UnwrapReflectVal(v interface{}) interface{} {
	switch val := v.(type) {
	case reflect.Value:
		return val.Interface()
	default:
		return val
	}
}

func toBool(reflVal reflect.Value) bool {
	if !reflVal.IsValid() {
		return false
	}

	switch reflVal.Kind() {
	case reflect.String:
		return reflVal.Len() != 0
	case reflect.Slice:
		return reflVal.Len() != 0
	case reflect.Chan, reflect.Map:
		return !reflVal.IsNil() && reflVal.Len() != 0
	case reflect.Func, reflect.Pointer, reflect.UnsafePointer, reflect.Interface:
		return !reflVal.IsNil()
	default:
		return true
	}
}

type PermissionKind int

const (
	ReadPerm PermissionKind = iota
	UpdatePerm
	CreatePerm
	DeletePerm
	UsePerm
	ConsumePerm
	ProvidePerm
)

func (kind PermissionKind) String() string {
	if kind < 0 || int(kind) >= len(PERMISSION_KIND_STRINGS) {
		return "<invalid permission kind>"
	}

	return PERMISSION_KIND_STRINGS[kind]
}

func PermissionKindFromString(s string) (PermissionKind, bool) {
	for i, perm := range PERMISSION_KIND_STRINGS {
		if s == perm {
			return PermissionKind(i), true
		}
	}

	return 0, false
}

type Permission interface {
	Kind() PermissionKind
	Includes(Permission) bool
	String() string
}

type NotAllowedError struct {
	Permission Permission
	Message    string
}

func (err NotAllowedError) Error() string {
	return err.Message
}

type Limitation struct {
	Name        string
	SimpleRate  SimpleRate
	ByteRate    ByteRate
	Total       int64
	DecrementFn func(time.Time) int64
}

type Limiter struct {
	limitation Limitation
	bucket     *TokenBucket
	contexts   []*Context
}

type LoadType int

const (
	ComputeLoad LoadType = iota
	IOLoad
)

type Context struct {
	executionStartTime   time.Time
	currentLoadType      LoadType
	grantedPermissions   []Permission
	forbiddenPermissions []Permission
	limitations          []Limitation
	limiters             map[string]*Limiter
	stackPermission      StackPermission
	hostAliases          map[string]interface{}
	namedPatterns        map[string]Matcher
}

func NewContext(permissions []Permission, forbiddenPermissions []Permission, limitations []Limitation) *Context {

	var stackPermission = StackPermission{maxHeight: DEFAULT_MAX_STACK_HEIGHT}
	//check permissions
	for _, perm := range permissions {
		switch p := perm.(type) {
		case StackPermission:
			if p.maxHeight > TRULY_MAX_STACK_HEIGHT {
				log.Panicln("context creation: invalid stack height permission")
			}
			stackPermission = p
		}
	}

	limiters := map[string]*Limiter{}

	ctx := &Context{}

	for _, l := range limitations {

		_, alreadyExist := limiters[l.Name]
		if alreadyExist {
			log.Panicf("context creation: duplicate limit '%s'\n", l.Name)
		}

		var increment int64 = 1
		if l.ByteRate != 0 {
			increment = int64(l.ByteRate)
		}

		if l.SimpleRate != 0 {
			increment = int64(l.SimpleRate)
		}

		var cap int64 = int64(l.SimpleRate)
		if cap == 0 {
			cap = int64(l.ByteRate)
		}

		if cap == 0 {
			cap = l.Total
		}

		limiters[l.Name] = &Limiter{
			contexts:   []*Context{ctx},
			limitation: l,
			//Buckets all have the same tick interval. Calculating the interval from the rate
			//can result in small values (< 5ms) that are too precise and cause issues.
			bucket: newBucket(TOKEN_BUCKET_INTERVAL, TOKEN_BUCKET_CAPACITY_SCALE*cap, increment, l.DecrementFn),
		}
	}

	*ctx = Context{
		executionStartTime:   time.Now(),
		grantedPermissions:   permissions,
		forbiddenPermissions: forbiddenPermissions,
		limitations:          limitations,
		limiters:             limiters,
		stackPermission:      stackPermission,
		hostAliases:          map[string]interface{}{},
		namedPatterns:        map[string]Matcher{},
	}

	return ctx
}

func (ctx *Context) HasPermission(perm Permission) bool {
	for _, forbiddenPerm := range ctx.forbiddenPermissions {
		if forbiddenPerm.Includes(perm) {
			return false
		}
	}

	for _, grantedPerm := range ctx.grantedPermissions {
		if grantedPerm.Includes(perm) {
			return true
		}
	}
	return false
}

func (ctx *Context) CheckHasPermission(perm Permission) error {
	if !ctx.HasPermission(perm) {
		return NotAllowedError{
			Permission: perm,
			Message:    fmt.Sprintf("not allowed, missing permission: %s", perm.String()),
		}
	}

	return nil
}

//Creates a new Context  with additional permissions
func (ctx *Context) NewWith(additionalPerms []Permission) (*Context, error) {

	var perms []Permission = make([]Permission, len(ctx.grantedPermissions))
	copy(perms, ctx.grantedPermissions)

top:
	for _, additonalPerm := range additionalPerms {
		for _, perm := range perms {
			if perm.Includes(additonalPerm) {
				continue top
			}
		}

		perms = append(perms, additonalPerm)
	}

	newCtx := NewContext(perms, ctx.forbiddenPermissions, ctx.limitations)
	return newCtx, nil
}

//Creates a new Context with the permissions passed as argument removed.
//The limiters are shared between the two contexts.
func (ctx *Context) NewWithout(removedPerms []Permission) (*Context, error) {

	var perms []Permission
	var forbiddenPerms []Permission

top:
	for _, perm := range ctx.grantedPermissions {
		for _, removedPerm := range removedPerms {
			if removedPerm.Includes(perm) {
				continue top
			}
		}

		perms = append(perms, perm)
	}

	newCtx := NewContext(perms, forbiddenPerms, nil)
	newCtx.limiters = ctx.limiters
	return newCtx, nil
}

func (ctx *Context) DropPermissions(droppedPermissions []Permission) {

	var perms []Permission

top:
	for _, perm := range ctx.grantedPermissions {
		for _, removedPerm := range droppedPermissions {
			if removedPerm.Includes(perm) {
				continue top
			}
		}

		perms = append(perms, perm)
	}

	ctx.grantedPermissions = perms
	ctx.forbiddenPermissions = append(ctx.forbiddenPermissions, droppedPermissions...)
}

func (ctx *Context) Take(name string, count int64) {

	scaledCount := TOKEN_BUCKET_CAPACITY_SCALE * count

	limiter, ok := ctx.limiters[name]
	if ok {
		if limiter.limitation.Total != 0 && limiter.bucket.avail < scaledCount {
			panic(fmt.Errorf("cannot take %v tokens from bucket (%s), only %v token(s) available", count, name, limiter.bucket.avail/TOKEN_BUCKET_CAPACITY_SCALE))
		}
		limiter.bucket.Take(scaledCount)
	}
}

func (ctx *Context) GetRate(name string) (ByteRate, error) {
	limiter, ok := ctx.limiters[name]
	if ok {
		return limiter.limitation.ByteRate, nil
	}
	return -1, fmt.Errorf("context: cannot get rate '%s': not present", name)
}

func (ctx *Context) resolveHostAlias(alias string) interface{} {
	host, ok := ctx.hostAliases[alias]
	if !ok {
		return nil
	}
	return host
}

func (ctx *Context) addHostAlias(alias string, host interface{}) {
	_, ok := ctx.hostAliases[alias]
	if ok {
		panic(errors.New("cannot register a host alias more than once"))
	}
	ctx.hostAliases[alias] = host
}

func (ctx *Context) resolveNamedPattern(name string) Matcher {
	pattern, ok := ctx.namedPatterns[name]
	if !ok {
		return nil
	}
	return pattern
}

func (ctx *Context) addNamedPattern(name string, pattern Matcher) {
	_, ok := ctx.namedPatterns[name]
	if ok {
		panic(errors.New("cannot register a pattern more than once"))
	}
	ctx.namedPatterns[name] = pattern
}

type IterationChange int

const (
	NoIterationChange IterationChange = iota
	BreakIteration
	ContinueIteration
)

type State struct {
	ScopeStack  []map[string]interface{}
	ReturnValue *interface{}
	IterationChange
	ctx        *Context
	constants  map[string]int
	Script     []rune
	ScriptName string
}

func (state State) GlobalScope() map[string]interface{} {
	return state.ScopeStack[0]
}

func (state State) CurrentScope() map[string]interface{} {
	return state.ScopeStack[len(state.ScopeStack)-1]
}

func (state *State) PushScope() {
	state.ScopeStack = append(state.ScopeStack, make(map[string]interface{}))
}

func (state *State) PopScope() {
	state.ScopeStack = state.ScopeStack[:len(state.ScopeStack)-1]
}

func Memb(value interface{}, name string) (interface{}, *reflect.Type, error) {
	switch v := value.(type) {
	case Object:
		return v[name], nil, nil
	case ExternalValue:
		if obj, ok := v.value.(Object); !ok {
			return nil, nil, errors.New("member expression: external value: only objects supported")
		} else {
			return ExtValOf(obj[name], v.state), nil, nil
		}
	case reflect.Value:

		var ptr reflect.Value

		if v.Kind() == reflect.Ptr {
			ptr = v
			v = v.Elem()
		}

		switch v.Kind() {
		case reflect.Struct:
			fieldValue := v.FieldByName(name)
			if fieldValue.IsValid() {
				return ValOf(fieldValue), nil, nil
			}
			fallthrough
		case reflect.Interface:
			method := v.MethodByName(name)
			if !method.IsValid() {
				if ptr.IsValid() {
					method = ptr.MethodByName(name)
				}
				if !method.IsValid() {
					return nil, nil, errors.New("property ." + name + " does not exist")
				}
			}
			receiverType := v.Type()
			return method, &receiverType, nil
		default:
			return nil, nil, errors.New("Cannot get property ." + name + " for a value of kind " + v.Kind().String())
		}

	default:
		return nil, nil, fmt.Errorf("cannot get property .%s of non object/Go value (type %T)", name, v)
	}
}

func AtIndex(value interface{}, index int) (interface{}, error) {
	switch v := value.(type) {
	case List:
		return v[index], nil
	case []interface{}:
		return v[index], nil
	case string:
		return v[index], nil
	case []byte:
		return v[index], nil
	case []rune:
		return v[index], nil
	default:
		return nil, fmt.Errorf("AtIndex: first argument has invalid type: %T", value)
	}
}

func SetAtIndex(value interface{}, index int, e interface{}) error {
	switch v := value.(type) {
	case List:
		v[index] = e
	case []interface{}:
		v[index] = e
	case []byte:
		v[index] = e.(byte)
	case []rune:
		v[index] = e.(rune)
	default:
		return fmt.Errorf("SetAtIndex: first argument has invalid type: %T", value)
	}
	return nil
}

func GetSlice(value interface{}, start, end int) (interface{}, error) {
	switch v := value.(type) {
	case List:
		end = min(end, len(v))
		return v[start:end], nil
	case []interface{}:
		end = min(end, len(v))
		return v[start:end], nil
	case string:
		end = min(end, len(v))
		return v[start:end], nil
	case []byte:
		end = min(end, len(v))
		return v[start:end], nil
	case []rune:
		end = min(end, len(v))
		return v[start:end], nil
	default:
		return nil, fmt.Errorf("GetSlice: first argument has invalid type: %T", value)
	}
}

func SetSlice(value interface{}, start, end int, slice interface{}) error {
	if start >= end {
		return fmt.Errorf("SetSlice: invalid arguments: start should be less than end")
	}

	switch v := value.(type) {
	case List:
		copy(v[start:end], slice.(List))
	case []interface{}:
		copy(v[start:end], slice.([]interface{}))
	case []byte:
		copy(v[start:end], slice.([]byte))
	case []rune:
		copy(v[start:end], slice.([]rune))
	default:
		return fmt.Errorf("SetSlice: first argument has invalid type: %T", value)
	}
	return nil
}

func NewState(ctx *Context, args ...map[string]interface{}) *State {
	state := &State{
		ScopeStack: []map[string]interface{}{
			{},
		},
		ctx:       ctx,
		constants: map[string]int{},
	}

	if state.ctx == nil {
		state.ctx = NewContext(nil, nil, []Limitation{
			{"http/upload", 0, ByteRate(100_000), 0, nil},
			{"http/download", 0, ByteRate(100_000), 0, nil},
			{"fs/read", 0, ByteRate(1_000_000), 0, nil},
			{"fs/write", 0, ByteRate(100_000), 0, nil},
		})
	}

	globalScope := state.GlobalScope()

	for _, arg := range args {
		for k, v := range arg {
			globalScope[k] = ValOf(v)
		}
	}

	return state
}

type TraversalAction int
type TraversalOrder int

const (
	Continue TraversalAction = iota
	Prune
	StopTraversal
)

type TraversalConfiguration struct {
	MaxDepth int
}

//Traverse a graph of values starting from v.
//Only objects & lists are considered source nodes, the other ones are sinks (leafs).
//A list of encountered source nodes is used to prevent cycling
func Traverse(v interface{}, fn func(interface{}) (TraversalAction, error), config TraversalConfiguration) (terror error) {
	encounteredSourceNodes := make(List, 0)
	depth := 0
	return traverse(v, fn, config, encounteredSourceNodes, depth)
}

func traverse(v interface{}, fn func(interface{}) (TraversalAction, error), config TraversalConfiguration, encounteredSourceNodes List, depth int) (terror error) {

	if depth > config.MaxDepth {
		panic(StopTraversal)
	}

	defer func() {
		if depth == 0 {
			val := recover()
			if val == StopTraversal {
				terror = nil
			} else if val != nil {
				panic(val)
			}
		}
	}()

	if v == nil {
		return nil
	}

	for _, e := range encounteredSourceNodes {

		switch eV := e.(type) {
		case Object:
			if obj, ok := v.(Object); ok && samePointer(eV, obj) {
				return nil
			}
		case List:
			if list, ok := v.(List); ok && len(list) == len(eV) && cap(list) == cap(eV) {
				header1 := (*reflect.SliceHeader)(unsafe.Pointer(&eV))
				header2 := (*reflect.SliceHeader)(unsafe.Pointer(&list))

				if header1.Data == header2.Data {
					return nil
				}
			}
		}

	}

	action, err := fn(v)
	if err != nil {
		return err
	}

	switch action {
	case Continue:
		break
	case Prune:
		return nil
	case StopTraversal:
		panic(StopTraversal)
	default:
		return fmt.Errorf("invalid traversal action: %v", action)
	}

	switch val := v.(type) {
	case Object:
		encounteredSourceNodes = append(encounteredSourceNodes, v)
		for _, propV := range val {
			if err := traverse(propV, fn, config, encounteredSourceNodes, depth+1); err != nil {
				return err
			}
		}
	case List:
		encounteredSourceNodes = append(encounteredSourceNodes, v)
		for _, elem := range val {
			if err := traverse(elem, fn, config, encounteredSourceNodes, depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}

//This functions performs a pre-order traversal on an AST (depth first).
func Walk(node Node, fn func(Node, Node, Node, []Node) (error, TraversalAction)) (err error) {
	defer func() {

		v := recover()

		switch val := v.(type) {
		case error:
			err = val
		case nil:
		case TraversalAction:
		default:
			panic(v)
		}
	}()

	ancestorChain := make([]Node, 0)
	walk(node, nil, &ancestorChain, fn)
	return
}

func walk(node, parent Node, ancestorChain *[]Node, fn func(Node, Node, Node, []Node) (error, TraversalAction)) {

	if reflect.ValueOf(node).IsNil() {
		return
	}

	if ancestorChain != nil {
		*ancestorChain = append((*ancestorChain), parent)
		defer func() {
			*ancestorChain = (*ancestorChain)[:len(*ancestorChain)-1]
		}()
	}

	var scopeNode = parent
	for _, a := range *ancestorChain {
		if isScopeContainerNode(a) {
			scopeNode = a
		}
	}

	err, action := fn(node, parent, scopeNode, *ancestorChain)

	if err != nil {
		panic(err)
	}

	switch action {
	case StopTraversal:
		panic(StopTraversal)
	case Prune:
		return
	}

	switch n := node.(type) {
	case *Module:
		if n.Requirements != nil {
			walk(n.Requirements.Object, node, ancestorChain, fn)
		}

		if n.GlobalConstantDeclarations != nil {
			walk(n.GlobalConstantDeclarations, node, ancestorChain, fn)
		}

		for _, stmt := range n.Statements {
			walk(stmt, node, ancestorChain, fn)
		}
	case *EmbeddedModule:
		if n.Requirements != nil {
			walk(n.Requirements.Object, node, ancestorChain, fn)
		}

		for _, stmt := range n.Statements {
			walk(stmt, node, ancestorChain, fn)
		}
	case *OptionExpression:
		walk(n.Value, node, ancestorChain, fn)
	case *PermissionDroppingStatement:
		walk(n.Object, node, ancestorChain, fn)
	case *ImportStatement:
		walk(n.Identifier, node, ancestorChain, fn)
		walk(n.URL, node, ancestorChain, fn)
		walk(n.ValidationString, node, ancestorChain, fn)
		walk(n.ArgumentObject, node, ancestorChain, fn)
		walk(n.GrantedPermissions, node, ancestorChain, fn)
	case *SpawnExpression:
		if n.GroupIdent != nil {
			walk(n.GroupIdent, node, ancestorChain, fn)
		}
		walk(n.Globals, node, ancestorChain, fn)
		walk(n.ExprOrVar, node, ancestorChain, fn)
		if n.GrantedPermissions != nil {
			walk(n.GrantedPermissions, node, ancestorChain, fn)
		}
	case *ListLiteral:
		for _, element := range n.Elements {
			walk(element, node, ancestorChain, fn)
		}
	case *Block:
		for _, stmt := range n.Statements {
			walk(stmt, node, ancestorChain, fn)
		}
	case *FunctionDeclaration:
		walk(n.Name, node, ancestorChain, fn)
		walk(n.Function, node, ancestorChain, fn)
	case *FunctionExpression:
		for _, p := range n.Parameters {
			walk(p, node, ancestorChain, fn)
		}
		walk(n.Body, node, ancestorChain, fn)

		if n.Requirements != nil {
			walk(n.Requirements.Object, node, ancestorChain, fn)
		}
	case *FunctionParameter:
		walk(n.Var, node, ancestorChain, fn)
	case *GlobalConstantDeclarations:
		for _, decl := range n.Declarations {
			walk(decl, node, ancestorChain, fn)
		}

	case *ObjectLiteral:
		for _, prop := range n.Properties {
			walk(&prop, node, ancestorChain, fn)
		}
		for _, el := range n.SpreadElements {
			walk(el, node, ancestorChain, fn)
		}
	case *ObjectProperty:
		if n.Key != nil {
			walk(n.Key, node, ancestorChain, fn)
		}

		walk(n.Value, node, ancestorChain, fn)
	case *ObjectPatternLiteral:
		for _, prop := range n.Properties {
			walk(&prop, node, ancestorChain, fn)
		}
	case *ListPatternLiteral:
		for _, elem := range n.Elements {
			walk(elem, node, ancestorChain, fn)
		}
	case *MemberExpression:
		walk(n.Left, node, ancestorChain, fn)
		walk(n.PropertyName, node, ancestorChain, fn)
	case *ExtractionExpression:
		walk(n.Object, node, ancestorChain, fn)
		walk(n.Keys, node, ancestorChain, fn)
	case *IndexExpression:
		walk(n.Indexed, node, ancestorChain, fn)
		walk(n.Index, node, ancestorChain, fn)
	case *SliceExpression:
		walk(n.Indexed, node, ancestorChain, fn)
		if n.StartIndex != nil {
			walk(n.StartIndex, node, ancestorChain, fn)
		}
		if n.EndIndex != nil {
			walk(n.EndIndex, node, ancestorChain, fn)
		}
	case *IdentifierMemberExpression:
		walk(n.Left, node, ancestorChain, fn)
		for _, p := range n.PropertyNames {
			walk(p, node, ancestorChain, fn)
		}
	case *KeyListExpression:
		for _, key := range n.Keys {
			walk(key, node, ancestorChain, fn)
		}
	case *BooleanConversionExpression:
		walk(n.Expr, node, ancestorChain, fn)
	case *Assignment:
		walk(n.Left, node, ancestorChain, fn)
		walk(n.Right, node, ancestorChain, fn)
	case *MultiAssignment:
		for _, vr := range n.Variables {
			walk(vr, node, ancestorChain, fn)
		}
		walk(n.Right, node, ancestorChain, fn)
	case *HostAliasDefinition:
		walk(n.Left, node, ancestorChain, fn)
		walk(n.Right, node, ancestorChain, fn)
	case *Call:
		walk(n.Callee, node, ancestorChain, fn)
		for _, arg := range n.Arguments {
			walk(arg, node, ancestorChain, fn)
		}
	case *IfStatement:
		walk(n.Test, node, ancestorChain, fn)
		walk(n.Consequent, node, ancestorChain, fn)
		if n.Alternate != nil {
			walk(n.Alternate, node, ancestorChain, fn)
		}
	case *ForStatement:
		if n.KeyIndexIdent != nil {
			walk(n.KeyIndexIdent, node, ancestorChain, fn)
			walk(n.ValueElemIdent, node, ancestorChain, fn)
		}

		walk(n.IteratedValue, node, ancestorChain, fn)
		walk(n.Body, node, ancestorChain, fn)
	case *ReturnStatement:
		if n.Expr != nil {
			walk(n.Expr, node, ancestorChain, fn)
		}

	case *BreakStatement:
		if n.Label != nil {
			walk(n.Label, node, ancestorChain, fn)
		}
	case *ContinueStatement:
		if n.Label != nil {
			walk(n.Label, node, ancestorChain, fn)
		}
	case *SwitchStatement:
		walk(n.Discriminant, node, ancestorChain, fn)
		for _, switcCase := range n.Cases {
			walk(switcCase, node, ancestorChain, fn)
		}
	case *MatchStatement:
		walk(n.Discriminant, node, ancestorChain, fn)
		for _, switcCase := range n.Cases {
			walk(switcCase, node, ancestorChain, fn)
		}
	case *Case:
		walk(n.Value, node, ancestorChain, fn)
		walk(n.Block, node, ancestorChain, fn)
	case *LazyExpression:
		walk(n.Expression, node, ancestorChain, fn)
	case *BinaryExpression:
		walk(n.Left, node, ancestorChain, fn)
		walk(n.Right, node, ancestorChain, fn)
	case *UpperBoundRangeExpression:
		walk(n.UpperBound, node, ancestorChain, fn)
	case *IntegerRangeLiteral:
		walk(n.LowerBound, node, ancestorChain, fn)
		walk(n.UpperBound, node, ancestorChain, fn)
	case *RuneRangeExpression:
		walk(n.Lower, node, ancestorChain, fn)
		walk(n.Upper, node, ancestorChain, fn)
	case *NamedSegmentPathPatternLiteral:
		for _, e := range n.Slices {
			walk(e, node, ancestorChain, fn)
		}
	case *AbsolutePathExpression:
		for _, e := range n.Slices {
			walk(e, node, ancestorChain, fn)
		}
	case *RelativePathExpression:
		for _, e := range n.Slices {
			walk(e, node, ancestorChain, fn)
		}
	case *URLExpression:
		walk(n.HostPart, node, ancestorChain, fn)
		walk(n.Path, node, ancestorChain, fn)
		for _, param := range n.QueryParams {
			walk(param, node, ancestorChain, fn)
		}
	case *URLQueryParameter:
		for _, val := range n.Value {
			walk(val, node, ancestorChain, fn)
		}
	case *RateLiteral:
		walk(n.Quantity, node, ancestorChain, fn)
		walk(n.Unit, node, ancestorChain, fn)
	case *PipelineStatement:
		for _, stage := range n.Stages {
			walk(stage.Expr, node, ancestorChain, fn)
		}
	case *PipelineExpression:
		for _, stage := range n.Stages {
			walk(stage.Expr, node, ancestorChain, fn)
		}
	case *PatternDefinition:
		walk(n.Left, node, ancestorChain, fn)
		walk(n.Right, node, ancestorChain, fn)
	case *PatternPiece:
		for _, element := range n.Elements {
			walk(element, node, ancestorChain, fn)
		}
	case *PatternPieceElement:
		walk(n.Expr, node, ancestorChain, fn)
	case *PatternUnion:
		for _, case_ := range n.Cases {
			walk(case_, node, ancestorChain, fn)
		}
	}

}

func shiftNodeSpans(node Node, offset int) {
	ancestorChain := make([]Node, 0)

	walk(node, nil, &ancestorChain, func(node, parent, scopeNode Node, ancestorChain []Node) (error, TraversalAction) {
		node.BasePtr().Span.Start += offset
		node.BasePtr().Span.End += offset
		return nil, Continue
	})
}

type globalVarInfo struct {
	isConst bool
}

//Check performs various checks on an AST, like checking that return, break and continue statements are not misplaced.
//Some checks are done while parsing : see the ParseModule function.
func Check(node Node) error {

	//key: *Module|*EmbeddedModule
	fnDecls := make(map[Node]map[string]int)

	//key: *Module|*EmbeddedModule|*Block
	globalVars := make(map[Node]map[string]globalVarInfo)

	//key: *Module|*EmbeddedModule|*Block
	localVars := make(map[Node]map[string]int)

	return Walk(node, func(n, parent, scopeNode Node, ancestorChain []Node) (error, TraversalAction) {

		switch node := n.(type) {
		case *QuantityLiteral:
			switch node.Unit {
			case "x", "s", "ms", "%", "ln", "kB", "MB", "GB":
			default:
				return errors.New("non supported unit: " + node.Unit), Continue
			}
		case *RateLiteral:

			unit1 := node.Quantity.Unit
			unit2 := node.Unit.Name

			switch unit2 {
			case "s":
				switch unit1 {
				case "x", "kB", "MB", "GB":
					return nil, Continue
				}
			}

			return errors.New("invalid rate literal"), Continue
		case *ObjectLiteral:
			indexKey := 0
			keys := map[string]bool{}

			for _, prop := range node.Properties {
				var k string

				var isExplicit bool

				switch n := prop.Key.(type) {
				case *StringLiteral:
					k = n.Value
					isExplicit = true
				case *IdentifierLiteral:
					k = n.Name
					isExplicit = true
				case nil:
					k = strconv.Itoa(indexKey)
					indexKey++
				}

				if prevIsExplicit, found := keys[k]; found {
					if isExplicit && !prevIsExplicit {
						return errors.New("An object literal explictly declares a property with key '" + k + "' but has the same implicit key"), Continue
					}
					return errors.New("duplicate key '" + k + "'"), Continue
				}

				keys[k] = isExplicit
			}

			for _, element := range node.SpreadElements {

				for _, key := range element.Extraction.Keys.Keys {
					_, found := keys[key.Name]
					if found {
						return errors.New("duplicate key '" + key.Name + "'"), Continue
					}
					keys[key.Name] = true
				}
			}

		case *SpawnExpression:
			switch n := node.ExprOrVar.(type) {
			case *EmbeddedModule, *Variable, *GlobalVariable:
			case *Call:
				if _, ok := n.Callee.(*IdentifierLiteral); ok {
					break
				}
				return errors.New("invalid spawn expression: the expression should be a global func call, an embedded module or a variable (that can be global)"), Continue
			default:
				return errors.New("invalid spawn expression: the expression should be a global func call, an embedded module or a variable (that can be global)"), Continue
			}
		case *GlobalConstantDeclarations:
			for _, decl := range node.Declarations {
				name := decl.Left.Name

				variables, ok := globalVars[parent]

				if !ok {
					variables = make(map[string]globalVarInfo)
					globalVars[parent] = variables
				}

				_, alreadyUsed := variables[name]
				if alreadyUsed {
					return fmt.Errorf("invalid constant declaration: '%s' is already used", name), Continue
				}
				variables[name] = globalVarInfo{isConst: true}
			}
		case *Assignment, *MultiAssignment:
			var names []string

			switch assignment := n.(type) {
			case *Assignment:

				switch left := assignment.Left.(type) {

				case *GlobalVariable:
					fns, ok := fnDecls[scopeNode]
					if ok {
						_, alreadyUsed := fns[left.Name]
						if alreadyUsed {
							return fmt.Errorf("invalid global variable assignment: '%s' is a declared function's name", left.Name), Continue
						}
					}

					variables, ok := globalVars[scopeNode]

					if !ok {
						variables = make(map[string]globalVarInfo)
						globalVars[scopeNode] = variables
					}

					varInfo, alreadyDefined := variables[left.Name]
					if alreadyDefined {
						if varInfo.isConst {
							return fmt.Errorf("invalid global variable assignment: '%s' is a constant", left.Name), Continue
						}
					} else {
						variables[left.Name] = globalVarInfo{isConst: false}
					}

				case *Variable:
					if left.Name == "" { //$
						return errors.New("invalid assignment: anonymous variable '$' cannot be assigned"), Continue
					}
					names = append(names, left.Name)
				case *IdentifierLiteral:
					names = append(names, left.Name)
				}

			case *MultiAssignment:
				for _, variable := range assignment.Variables {
					names = append(names, variable.(*IdentifierLiteral).Name)
				}
			}

			for _, name := range names {
				variables, ok := localVars[scopeNode]

				if !ok {
					variables = make(map[string]int)
					localVars[scopeNode] = variables
				}

				variables[name] = 0
			}

		case *ForStatement:
			//TODO: !! the variables should not be considered defined after the statement !!

			variables, ok := localVars[scopeNode]
			if !ok {
				variables = make(map[string]int)
				localVars[scopeNode] = variables
			}
			if node.KeyIndexIdent != nil {
				variables[node.KeyIndexIdent.Name] = 0
				variables[node.ValueElemIdent.Name] = 0
			}

		case *FunctionDeclaration:

			switch parent.(type) {
			case *Module, *EmbeddedModule:
				fns, ok := fnDecls[parent]
				globVars, globalOk := globalVars[parent]

				if !ok {
					fns = make(map[string]int)
					fnDecls[parent] = fns
				}

				if globalOk {
					_, alreadyUsed := globVars[node.Name.Name]
					if alreadyUsed {
						return fmt.Errorf("invalid function declaration: a global variable named '%s' exist", node.Name.Name), Continue
					}
				}

				_, alreadyDeclared := fns[node.Name.Name]
				if alreadyDeclared {
					return fmt.Errorf("invalid function declaration: %s is already declared", node.Name.Name), Continue
				}
				fns[node.Name.Name] = 0
			default:
				return errors.New("invalid function declaration: a function declaration should be a top level statement in a module (embedded or not)"), Continue
			}
		case *FunctionExpression:
			parameters := make(map[string]int)
			localVars[node] = parameters

			for _, p := range node.Parameters {
				parameters[p.Var.Name] = 0
			}

		case *BreakStatement, *ContinueStatement:

			forStmtIndex := -1

			//we search for the last for statement in the ancestor chain
			for i := len(ancestorChain) - 1; i >= 0; i-- {
				_, isForStmt := ancestorChain[i].(*ForStatement)
				if isForStmt {
					forStmtIndex = i
					break
				}
			}

			if forStmtIndex < 0 {
				return fmt.Errorf("invalid break/continue statement: should be in a for statement"), Continue
			}

			for i := forStmtIndex + 1; i < len(ancestorChain); i++ {
				switch ancestorChain[i].(type) {
				case *IfStatement, *SwitchStatement, *MatchStatement, *Block:
				default:
					return fmt.Errorf("invalid break/continue statement: should be in a for statement"), Continue
				}
			}
		case *NamedSegmentPathPatternLiteral:
			//define the variables named after groups if the literal is used as a case in a match statement

			if _, isCase := parent.(*Case); isCase {

				stmt := ancestorChain[len(ancestorChain)-2]
				_, isMatchStmt := stmt.(*MatchStatement)
				if !isMatchStmt {
					break
				}

				variables, ok := localVars[scopeNode]

				if !ok {
					variables = make(map[string]int)
					localVars[scopeNode] = variables
				}

				for _, slice := range node.Slices {

					if variable, isVar := slice.(*Variable); isVar {
						variables[variable.Name] = 0
					}
				}
			}

		case *Variable:
			if node.Name == "" {
				break
			}

			if _, isNamedSegmentPathLiteral := parent.(*NamedSegmentPathPatternLiteral); isNamedSegmentPathLiteral {
				break
			}

			if _, isLazyExpr := scopeNode.(*LazyExpression); isLazyExpr {
				break
			}

			variables, ok := localVars[scopeNode]

			if !ok {
				return fmt.Errorf("local variable %s is not defined", node.Name), Continue
			}

			_, exist := variables[node.Name]
			if !exist {
				return fmt.Errorf("local variable %s is not defined", node.Name), Continue
			}
		}

		return nil, Continue
	})
}

func getQuantity(value float64, unit string) interface{} {
	switch unit {
	case "x":
		return value
	case "s":
		return reflect.ValueOf(time.Duration(value) * time.Second)
	case "ms":
		return reflect.ValueOf(time.Duration(value) * time.Millisecond)
	case "%":
		return value / 100
	case "ln":
		return LineCount(int(value))
	case "kB":
		return 1_000 * ByteCount(int(value))
	case "MB":
		return 1_000_000 * ByteCount(int(value))
	case "GB":
		return 1_000_000_000 * ByteCount(int(value))
	default:
		panic("unsupported unit " + unit)
	}
}

type SequenceStringPattern struct {
	regexp   *regexp.Regexp
	node     Node
	elements []StringPatternElement
}

func (patt SequenceStringPattern) Test(v interface{}) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	return patt.regexp.MatchString(str)
}

func (patt SequenceStringPattern) Regex() string {
	return patt.regexp.String()
}

func (patt SequenceStringPattern) Random() interface{} {
	s := bytes.NewBufferString("")
	for _, e := range patt.elements {
		s.WriteString(e.Random().(string))
	}

	return s.String()
}

type UnionStringPattern struct {
	regexp *regexp.Regexp
	node   Node
	cases  []StringPatternElement
}

func (patt UnionStringPattern) Test(v interface{}) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	return patt.regexp.MatchString(str)
}

func (patt UnionStringPattern) Regex() string {
	return patt.regexp.String()
}

func (patt UnionStringPattern) Random() interface{} {
	if len(patt.cases) == 1 {
		return patt.cases[0].Random()
	}

	i := rand.Intn(len(patt.cases) - 1)
	return patt.cases[i].Random()
}

type RuneRangeStringPattern struct {
	regexp *regexp.Regexp
	node   Node
	runes  RuneRange
}

func (patt RuneRangeStringPattern) Test(v interface{}) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	return patt.regexp.MatchString(str)
}

func (patt RuneRangeStringPattern) Regex() string {
	return patt.regexp.String()
}

func (patt RuneRangeStringPattern) Random() interface{} {
	return string(patt.runes.Random().(rune))
}

type StringPatternElement interface {
	Matcher
	GenerativePattern
	Regex() string
}

type RepeatedPatternElement struct {
	regexp            *regexp.Regexp
	ocurrenceModifier OcurrenceCountModifier
	exactCount        int
	element           StringPatternElement
}

func (patt RepeatedPatternElement) Test(v interface{}) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	return patt.regexp.MatchString(str)
}

func (patt RepeatedPatternElement) Regex() string {
	return patt.regexp.String()
}

func (patt RepeatedPatternElement) Random() interface{} {
	buff := bytes.NewBufferString("")

	minCount := patt.exactCount
	maxCount := patt.exactCount

	switch patt.ocurrenceModifier {
	case ExactOcurrence:
		//ok
	case ExactlyOneOcurrence:
		minCount = 1
		maxCount = 1
	case ZeroOrMoreOcurrence:
		minCount = 0
		maxCount = MAX_PATTERN_OCCURRENCE_COUNT
	case AtLeastOneOcurrence:
		minCount = 1
		maxCount = MAX_PATTERN_OCCURRENCE_COUNT
	case OptionalOcurrence:
		minCount = 0
		maxCount = 1
	}

	count := minCount + rand.Intn(int(maxCount-minCount+1))

	for i := 0; i < count; i++ {
		buff.WriteString(patt.element.Random().(string))
	}

	return buff.String()
}

func CompilePatternNode(node Node, state *State) (Matcher, error) {
	switch n := node.(type) {
	case *ObjectPatternLiteral:
		pattern, err := Eval(n, state)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate an object pattern literal: %s", err.Error())
		}
		objPattern, ok := pattern.(*ObjectPattern)
		if !ok {
			return nil, fmt.Errorf("failed to evaluate an object pattern literal: %s", err.Error())
		}

		return objPattern, nil
	case *ListPatternLiteral:
		pattern, err := Eval(n, state)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate an object pattern literal: %s", err.Error())
		}
		listPattern, ok := pattern.(*ListPattern)
		if !ok {
			return nil, fmt.Errorf("failed to evaluate an object pattern literal: %s", err.Error())
		}

		return listPattern, nil
	case *PatternPiece:
		if n.Kind == StringPattern {
			return CompileStringPatternNode(node, state)
		}
		return nil, fmt.Errorf("failed to compile a pattern node of type %T", node)
	case *PatternUnion:
		return CompileStringPatternNode(n, state)
	case *StringLiteral, *RuneLiteral, *RuneRangeExpression, *PatternIdentifierLiteral:
		return CompileStringPatternNode(n, state)
	default:
		return nil, fmt.Errorf("failed to compile a pattern node of type %T", node)
	}
}

func CompileStringPatternNode(node Node, state *State) (StringPatternElement, error) {
	switch v := node.(type) {
	case *StringLiteral:
		return ExactSimpleValueMatcher{v.Value}, nil
	case *RuneLiteral:
		return ExactSimpleValueMatcher{v.Value}, nil
	case *RuneRangeExpression:
		lower := v.Lower.Value
		upper := v.Upper.Value

		return &RuneRangeStringPattern{
			regexp: regexp.MustCompile(fmt.Sprintf("[%c-%c]", lower, upper)),
			node:   node,
			runes: RuneRange{
				Start: lower,
				End:   upper,
			},
		}, nil
	case *PatternIdentifierLiteral:
		pattern, err := Eval(v, state)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate a pattern identifier literal: %s", err.Error())
		}

		stringPatternElem, ok := pattern.(StringPatternElement)
		if !ok {
			return nil, fmt.Errorf("not a string pattern element: %T", pattern)
		}

		return stringPatternElem, nil
	case *PatternUnion:
		regex := bytes.NewBufferString("(")
		var cases []StringPatternElement

		for i, case_ := range v.Cases {

			if i > 0 {
				regex.WriteRune('|')
			}
			patternElement, err := CompileStringPatternNode(case_, state)
			if err != nil {
				return nil, fmt.Errorf("failed to compile a pattern element: %s", err.Error())
			}

			regex.WriteString(patternElement.Regex())
			cases = append(cases, patternElement)
		}

		regex.WriteRune(')')

		return &UnionStringPattern{
			regexp: regexp.MustCompile(regex.String()),
			node:   node,
			cases:  cases,
		}, nil
	case *PatternPiece:
		regex := bytes.NewBufferString("")
		var subpatterns []StringPatternElement

		for _, element := range v.Elements {
			subpatternRegexBuff := bytes.NewBufferString("")
			subpatternRegexBuff.WriteRune('(')

			patternElement, err := CompileStringPatternNode(element.Expr, state)
			if err != nil {
				return nil, fmt.Errorf("failed to compile a pattern piece: %s", err.Error())
			}

			subpatternRegexBuff.WriteString(patternElement.Regex())
			subpatternRegexBuff.WriteRune(')')

			switch element.Ocurrence {
			case AtLeastOneOcurrence:
				subpatternRegexBuff.WriteRune('+')
			case ZeroOrMoreOcurrence:
				subpatternRegexBuff.WriteRune('*')
			case OptionalOcurrence:
				subpatternRegexBuff.WriteRune('?')
			case ExactOcurrence:
				subpatternRegexBuff.WriteRune('{')
				subpatternRegexBuff.WriteString(strconv.Itoa(element.ExactOcurrenceCount))
				subpatternRegexBuff.WriteRune('}')
			}

			subpatternRegex := subpatternRegexBuff.String()
			regex.WriteString(subpatternRegex)

			if element.Ocurrence == ExactlyOneOcurrence {
				subpatterns = append(subpatterns, patternElement)
			} else {
				subpatterns = append(subpatterns, RepeatedPatternElement{
					regexp:            regexp.MustCompile(subpatternRegex),
					ocurrenceModifier: element.Ocurrence,
					exactCount:        element.ExactOcurrenceCount,
					element:           patternElement,
				})
			}
		}

		return &SequenceStringPattern{
			regexp:   regexp.MustCompile(regex.String()),
			node:     node,
			elements: subpatterns,
		}, nil
	default:
		return nil, fmt.Errorf("cannot compile string pattern element: %T", v)
	}
}

type NamedSegmentPathPattern struct {
	node *NamedSegmentPathPatternLiteral
}

func (patt NamedSegmentPathPattern) Test(v interface{}) bool {
	ok, _ := patt.MatchGroups(v)
	return ok
}

func (patt NamedSegmentPathPattern) MatchGroups(v interface{}) (bool, map[string]interface{}) {
	pth, ok := v.(Path)
	if !ok {
		return false, nil
	}

	str := string(pth)
	i := 0
	groups := make(map[string]interface{})

	for index, s := range patt.node.Slices {

		if i >= len(str) {
			return false, nil
		}

		switch n := s.(type) {
		case *PathSlice:
			if i+len(n.Value) > len(str) {
				return false, nil
			}
			if str[i:i+len(n.Value)] != n.Value {
				return false, nil
			}
			i += len(n.Value)
		case *Variable:
			segmentEnd := strings.Index(str[i:], "/")
			if segmentEnd < 0 {
				if index < len(patt.node.Slices)-1 {
					return false, nil
				}
				groups[n.Name] = str[i:]
				return true, groups
			} else if index == len(patt.node.Slices)-1 { //if $var$ is at the end of the pattern there should not be a '/'
				return false, nil
			} else {
				groups[n.Name] = str[i : i+segmentEnd]
				i += segmentEnd
			}
		}
	}

	if i == len(str) {
		return true, groups
	}

	return false, nil
}

type EntryMatcher struct {
	Key   Matcher
	Value Matcher
}

type ObjectPattern struct {
	EntryMatchers map[string]Matcher
}

func (patt ObjectPattern) Test(v interface{}) bool {
	obj, ok := v.(Object)
	if !ok {
		return false
	}
	for key, valueMatcher := range patt.EntryMatchers {
		value, ok := obj[key]
		if !ok || !valueMatcher.Test(value) {
			return false
		}
	}
	return true
}

type ListPattern struct {
	ElementMatchers []Matcher
}

func (patt ListPattern) Test(v interface{}) bool {
	list, ok := v.(List)
	if !ok {
		return false
	}
	if len(list) != len(patt.ElementMatchers) {
		return false
	}
	for i, elementMatcher := range patt.ElementMatchers {
		if !ok || !elementMatcher.Test(list[i]) {
			return false
		}
	}
	return true
}

//MustEval calls Eval and panics if there is an error.
func MustEval(node Node, state *State) interface{} {
	res, err := Eval(node, state)
	if err != nil {
		panic(err)
	}
	return res
}

//Evaluates a node, panics are always recovered so this function should not panic.
func Eval(node Node, state *State) (result interface{}, err error) {

	defer func() {
		if e := recover(); e != nil {
			if er, ok := e.(error); ok {
				err = fmt.Errorf("eval: error: %s %s", er, debug.Stack())
			} else {
				err = fmt.Errorf("eval: %s", e)
			}
		}

		if err != nil && len(state.Script) != 0 && state.ScriptName != "" {
			line := 1
			col := 1
			i := 0

			for i < node.Base().Span.Start {
				if state.Script[i] == '\n' {
					line++
					col = 1
				} else {
					col++
				}

				i++
			}
			if !strings.HasPrefix(err.Error(), state.ScriptName) {
				err = fmt.Errorf("%s:%d:%d: %s", state.ScriptName, line, col, err)
			}
		}
	}()

	switch n := node.(type) {
	case *BooleanLiteral:
		return n.Value, nil
	case *IntLiteral:
		return n.Value, nil
	case *FloatLiteral:
		return n.Value, nil
	case *QuantityLiteral:
		//This implementation does not allow custom units.
		//Should it be entirely external ? Should most common units be still handled here ?
		return getQuantity(n.Value, n.Unit), nil
	case *RateLiteral:
		q, err := Eval(n.Quantity, state)
		if err != nil {
			return nil, err
		}

		switch qv := q.(type) {
		case ByteCount:
			if n.Unit.Name != "s" {
				return nil, errors.New("invalid unit " + n.Unit.Name)
			}
			return ByteRate(int(qv)), nil
		case float64:
			if n.Unit.Name != "s" {
				return nil, errors.New("invalid unit " + n.Unit.Name)
			}
			return SimpleRate(int(qv)), nil
		}

		return nil, fmt.Errorf("invalid quantity type: %T", q)
	case *StringLiteral:
		return n.Value, nil
	case *RuneLiteral:
		return n.Value, nil
	case *IdentifierLiteral:
		return Identifier(n.Name), nil
	case *AbsolutePathLiteral:
		return Path(n.Value), nil
	case *RelativePathLiteral:
		return Path(n.Value), nil
	case *AbsolutePathPatternLiteral:
		return PathPattern(n.Value), nil
	case *RelativePathPatternLiteral:
		return PathPattern(n.Value), nil
	case *NamedSegmentPathPatternLiteral:
		return NamedSegmentPathPattern{n}, nil
	case *RegularExpressionLiteral:
		return RegexMatcher{regexp.MustCompile(n.Value)}, nil

	case *PathSlice:
		return n.Value, nil
	case *URLQueryParameterSlice:
		return n.Value, nil
	case *FlagLiteral:
		return Option{Name: n.Name, Value: true}, nil
	case *OptionExpression:
		value, err := Eval(n.Value, state)
		if err != nil {
			return nil, err
		}
		return Option{Name: n.Name, Value: value}, nil
	case *AbsolutePathExpression, *RelativePathExpression:

		var slices []Node

		switch pexpr := n.(type) {
		case *AbsolutePathExpression:
			slices = pexpr.Slices
		case *RelativePathExpression:
			slices = pexpr.Slices
		}

		pth := ""

		for _, node := range slices {
			_, isStaticPathSlice := node.(*PathSlice)
			pathSlice, err := Eval(node, state)
			if err != nil {
				return nil, err
			}
			switch s := pathSlice.(type) {
			case string:
				if !isStaticPathSlice && (strings.Contains(s, "..") || strings.ContainsRune(s, '/') || strings.ContainsRune(s, '\\')) {
					return nil, errors.New("path expression: error: result should not contain the substring '..' or '/' or '\\' ")
				}
				pth += s
			case Path:
				str := string(s)
				if str[0] == '/' {
					str = "./" + str
				}
				pth = path.Join(pth, str)
			default:
				return nil, errors.New("path expression: path slices should have a string value")
			}
		}

		if strings.Contains(pth, "..") {
			return nil, errors.New("path expression: error: result should not contain the substring '..' ")
		}

		if !HasPathLikeStart(pth) {
			pth = "./" + pth
		}

		if len(pth) >= 2 {
			if pth[0] == '/' && pth[1] == '/' {
				pth = pth[1:]
			}
		}

		return Path(pth), nil
	case *URLLiteral:
		return URL(n.Value), nil
	case *HTTPHostLiteral:
		return HTTPHost(n.Value), nil
	case *AtHostLiteral:
		return state.ctx.resolveHostAlias(n.Value[1:]), nil
	case *HTTPHostPatternLiteral:
		return HTTPHostPattern(n.Value), nil
	case *URLPatternLiteral:
		return URLPattern(n.Value), nil
	case *URLExpression:
		pth, err := Eval(n.Path, state)
		if err != nil {
			return nil, err
		}

		queryBuff := bytes.NewBufferString("")
		if len(n.QueryParams) != 0 {
			queryBuff.WriteRune('?')
		}

		for i, p := range n.QueryParams {

			if i != 0 {
				queryBuff.WriteRune('&')
			}

			param := p.(*URLQueryParameter)
			queryBuff.Write([]byte(param.Name))
			queryBuff.WriteRune('=')

			for _, slice := range param.Value {
				val, err := Eval(slice, state)
				if err != nil {
					return nil, err
				}
				queryBuff.WriteString(val.(string))
			}
		}

		host, err := Eval(n.HostPart, state)
		if err != nil {
			return nil, err
		}

		return URL(fmt.Sprint(host) + string(pth.(Path)) + queryBuff.String()), nil
	case *NilLiteral:
		return nil, nil
	case *Variable:
		v, ok := state.CurrentScope()[n.Name]

		if !ok {
			return nil, errors.New("variable " + n.Name + " is not declared")
		}
		return v, nil
	case *GlobalVariable:
		err := state.ctx.CheckHasPermission(GlobalVarPermission{Kind_: ReadPerm, Name: n.Name})
		if err != nil {
			return nil, err
		}
		v, ok := state.GlobalScope()[n.Name]

		if !ok {
			return nil, errors.New("global variable " + n.Name + " is not declared")
		}
		return v, nil
	case *ReturnStatement:
		if n.Expr == nil {
			return nil, nil
		}

		value, err := Eval(n.Expr, state)
		if err != nil {
			return nil, err
		}

		state.ReturnValue = &value
		return nil, nil
	case *BreakStatement:
		state.IterationChange = BreakIteration
		return nil, nil
	case *ContinueStatement:
		state.IterationChange = ContinueIteration
		return nil, nil
	case *Call:
		return CallFunc(n.Callee, state, n.Arguments, n.Must)
	case *PipelineStatement, *PipelineExpression:

		var stages []*PipelineStage

		switch e := n.(type) {
		case *PipelineStatement:
			stages = e.Stages
		case *PipelineExpression:
			stages = e.Stages
		}

		scope := state.CurrentScope()
		if savedAnonymousValue, hasValue := scope[""]; hasValue {
			defer func() {
				scope[""] = savedAnonymousValue
			}()
		}

		var res interface{}

		for _, stage := range stages {
			res, err = Eval(stage.Expr, state)
			if err != nil {
				return nil, err
			}
			scope[""] = res
		}

		return res, nil
	case *Assignment:

		switch lhs := n.Left.(type) {
		case *Variable:
			name := lhs.Name
			right, err := Eval(n.Right, state)
			if err != nil {
				return nil, err
			}

			state.CurrentScope()[name] = right
		case *IdentifierLiteral:
			name := lhs.Name
			right, err := Eval(n.Right, state)
			if err != nil {
				return nil, err
			}

			state.CurrentScope()[name] = right
		case *GlobalVariable:
			name := lhs.Name
			scope := state.GlobalScope()
			_, alreadyDefined := scope[name]
			if alreadyDefined {
				if _, ok := state.constants[name]; ok {
					return nil, errors.New("attempt to assign a constant global")
				}

				err := state.ctx.CheckHasPermission(GlobalVarPermission{Kind_: UpdatePerm, Name: name})
				if err != nil {
					return nil, err
				}
			} else {
				err = state.ctx.CheckHasPermission(GlobalVarPermission{Kind_: CreatePerm, Name: name})
				if err != nil {
					return nil, err
				}
			}

			right, err := Eval(n.Right, state)
			if err != nil {
				return nil, err
			}

			state.CurrentScope()[name] = right
			scope[name] = right
		case *MemberExpression:
			object, err := Eval(lhs.Left, state)
			if err != nil {
				return nil, err
			}

			right, err := Eval(n.Right, state)
			if err != nil {
				return nil, err
			}

			object.(Object)[lhs.PropertyName.Name] = right
		case *IndexExpression:
			slice, err := Eval(lhs.Indexed, state)
			if err != nil {
				return nil, err
			}

			index, err := Eval(lhs.Index, state)
			if err != nil {
				return nil, err
			}

			right, err := Eval(n.Right, state)
			if err != nil {
				return nil, err
			}

			return nil, SetAtIndex(slice, index.(int), right)
		case *SliceExpression:
			slice, err := Eval(lhs.Indexed, state)
			if err != nil {
				return nil, err
			}

			startIndex, err := Eval(lhs.StartIndex, state)
			if err != nil {
				return nil, err
			}

			endIndex, err := Eval(lhs.EndIndex, state)
			if err != nil {
				return nil, err
			}

			right, err := Eval(n.Right, state)
			if err != nil {
				return nil, err
			}

			return nil, SetSlice(slice, startIndex.(int), endIndex.(int), right)
		default:
			return nil, fmt.Errorf("invalid assignment: left hand side is a(n) %T", n.Left)
		}

		return nil, nil
	case *MultiAssignment:
		right, err := Eval(n.Right, state)

		if err != nil {
			return nil, err
		}

		rightValue := ToReflectVal(right)
		scopeValue := reflect.ValueOf(state.CurrentScope())

		for i, var_ := range n.Variables {
			elemValue := rightValue.Index(i)

			keyValue := reflect.ValueOf(var_.(*IdentifierLiteral).Name)
			scopeValue.SetMapIndex(keyValue, elemValue)
		}

		return nil, nil
	case *HostAliasDefinition:
		name := n.Left.Value[1:]
		value, err := Eval(n.Right, state)
		if err != nil {
			return nil, err
		}
		state.ctx.addHostAlias(name, value)

		return nil, nil
	case *Module:
		state.ScopeStack = state.ScopeStack[:1] //we only keep the global scope
		state.PushScope()
		state.ReturnValue = nil
		defer func() {
			state.ReturnValue = nil
			state.IterationChange = NoIterationChange
			state.PopScope()
		}()

		//CONSTANTS
		if n.GlobalConstantDeclarations != nil {
			globalScope := state.GlobalScope()
			for _, decl := range n.GlobalConstantDeclarations.Declarations {
				name := decl.Left.Name
				globalScope[name] = MustEval(decl.Right, nil)
				state.constants[name] = 0
			}
		}

		//STATEMENTS

		if len(n.Statements) == 1 {
			res, err := Eval(n.Statements[0], state)
			if err != nil {
				return nil, err
			}
			if state.ReturnValue != nil {
				return *state.ReturnValue, nil
			}

			return res, nil
		}

		for _, stmt := range n.Statements {
			_, err = Eval(stmt, state)

			if err != nil {
				return nil, err
			}
			if state.ReturnValue != nil {
				return *state.ReturnValue, nil
			}
		}

		return nil, nil
	case *EmbeddedModule:
		return ValOf(&Module{
			NodeBase:     n.NodeBase,
			Requirements: n.Requirements,
			Statements:   n.Statements,
		}), nil
	case *Block:
	loop:
		for _, stmt := range n.Statements {
			_, err := Eval(stmt, state)
			if err != nil {
				return nil, err
			}

			if state.ReturnValue != nil {
				return nil, nil
			}

			switch state.IterationChange {
			case BreakIteration, ContinueIteration:
				break loop
			}
		}
		return nil, nil
	case *PermissionDroppingStatement:
		perms, _ := n.Object.PermissionsLimitations(nil, state, nil)
		state.ctx.DropPermissions(perms)
		return nil, nil
	case *ImportStatement:
		varPerm := GlobalVarPermission{ReadPerm, n.Identifier.Name}
		if err := state.ctx.CheckHasPermission(varPerm); err != nil {
			return nil, fmt.Errorf("import: %s", err.Error())
		}

		url_, err := Eval(n.URL, state)
		if err != nil {
			return nil, err
		}

		httpPerm := HttpPermission{ReadPerm, url_.(URL)}
		if err := state.ctx.CheckHasPermission(httpPerm); err != nil {
			return nil, fmt.Errorf("import: %s", err.Error())
		}

		validationString, err := Eval(n.ValidationString, state)
		if err != nil {
			return nil, err
		}

		argObj, err := Eval(n.ArgumentObject, state)
		if err != nil {
			return nil, err
		}

		perms, _ := n.GrantedPermissions.PermissionsLimitations(nil, nil, nil)
		for _, perm := range perms {
			if err := state.ctx.CheckHasPermission(perm); err != nil {
				return nil, fmt.Errorf("import: cannot allow permission: %s", err.Error())
			}
		}

		mod, err := downloadAndParseModule(url_.(URL), validationString.(string))
		if err != nil {
			return nil, fmt.Errorf("import: cannot import module: %s", err.Error())
		}

		globals := map[string]interface{}(argObj.(Object))

		routineCtx := NewContext(perms, nil, nil)
		routineCtx.limiters = state.ctx.limiters

		routine, err := spawnRoutine(state, globals, mod, routineCtx)
		if err != nil {
			return nil, fmt.Errorf("import: %s", err.Error())
		}

		//TODO: add timeout
		result, err := routine.WaitResult(state.ctx)
		if err != nil {
			return nil, fmt.Errorf("import: module failed: %s", err.Error())
		}

		state.GlobalScope()[n.Identifier.Name] = ValOf(result)
		return nil, nil
	case *SpawnExpression:
		var group *RoutineGroup
		if n.GroupIdent != nil {
			name := n.GroupIdent.Name
			scope := state.CurrentScope()
			if val, present := scope[name]; present {
				refVal := UnwrapReflectVal(val)

				if rtGroup, ok := refVal.(*RoutineGroup); ok {
					group = rtGroup
				} else {
					panic(errors.New("a routine group has the the name of a pre-existing, non group variable"))
				}
			} else {
				group = &RoutineGroup{}
			}
		}

		var moduleOrCall Node
		var ctx *Context
		var actualGlobals map[string]interface{}

		switch n.ExprOrVar.(type) {
		case *Call:
			if n.GrantedPermissions == nil {
				newCtx, err := state.ctx.NewWithout([]Permission{
					GlobalVarPermission{ReadPerm, "*"},
					GlobalVarPermission{UpdatePerm, "*"},
					GlobalVarPermission{CreatePerm, "*"},

					RoutinePermission{CreatePerm},
				})
				if err != nil {
					return nil, fmt.Errorf("spawn expression: new context: %s", err.Error())
				}
				ctx = newCtx
			}

			moduleOrCall = n.ExprOrVar
			actualGlobals = state.GlobalScope()
		case *EmbeddedModule, *Variable, *GlobalVariable:
			actualGlobals = make(map[string]interface{})
			globals, err := Eval(n.Globals, state)

			if err != nil {
				return nil, err
			}

			switch g := globals.(type) {
			case Object:
				for k, v := range g {
					actualGlobals[k] = ExtValOf(v, state)
				}
			case KeyList:
				for _, name := range g {
					actualGlobals[name] = state.GlobalScope()[name]
				}
			case nil:
				break
			default:
				return nil, fmt.Errorf("spawn expression: globals: only objects and keylists are supported, not %T", g)
			}

			expr, err := Eval(n.ExprOrVar, state)
			if err != nil {
				return nil, err
			}

			moduleOrCall = expr.(*Module)

		}

		if n.GrantedPermissions != nil {
			perms, _ := n.GrantedPermissions.PermissionsLimitations(nil, state, nil)
			for _, perm := range perms {
				if err := state.ctx.CheckHasPermission(perm); err != nil {
					return nil, fmt.Errorf("spawn: cannot allow permission: %s", err.Error())
				}
			}
			ctx = NewContext(perms, nil, nil)
			ctx.limiters = state.ctx.limiters
		}

		routine, err := spawnRoutine(state, actualGlobals, moduleOrCall, ctx)
		if err != nil {
			return nil, err
		}

		if group != nil {
			group.add(routine)
			state.CurrentScope()[n.GroupIdent.Name] = ValOf(group)
		}

		return ValOf(routine), nil
	case *ObjectLiteral:
		obj := Object{}

		indexKey := 0
		for _, p := range n.Properties {
			v, err := Eval(p.Value, state)
			if err != nil {
				return nil, err
			}

			var k string

			switch n := p.Key.(type) {
			case *StringLiteral:
				k = n.Value
				_, err := strconv.ParseUint(k, 10, 32)
				if err == nil {
					//see Check function
					indexKey++
				}
			case *IdentifierLiteral:
				k = n.Name
			case nil:
				k = strconv.Itoa(indexKey)
				indexKey++
			default:
				return nil, fmt.Errorf("invalid key type %T", n)
			}

			obj[k] = v
		}

		for _, el := range n.SpreadElements {
			evaluatedElement, err := Eval(el.Extraction, state)
			if err != nil {
				return nil, err
			}

			object := evaluatedElement.(Object)

			for _, key := range el.Extraction.Keys.Keys {
				obj[key.Name] = object[key.Name]
			}
		}

		if indexKey != 0 {
			obj[IMPLICIT_KEY_LEN_KEY] = indexKey
		}

		return obj, nil
	case *ListLiteral:
		list := make(List, len(n.Elements))

		for i, en := range n.Elements {
			e, err := Eval(en, state)
			if err != nil {
				return nil, err
			}

			list[i] = e
		}

		return list, nil
	case *IfStatement:
		test, err := Eval(n.Test, state)
		if err != nil {
			return nil, err
		}

		if boolean, ok := test.(bool); ok {
			var err error
			if boolean {
				_, err = Eval(n.Consequent, state)
			} else if n.Alternate != nil {
				_, err = Eval(n.Alternate, state)
			}

			if err != nil {
				return nil, err
			}

			return nil, nil
		} else {
			return nil, fmt.Errorf("if statement test is not a boolean but a %T", test)
		}
	case *ForStatement:
		iteratedValue, err := Eval(n.IteratedValue, state)
		if err != nil {
			return nil, err
		}

		var kVarname string
		var eVarname string

		if n.KeyIndexIdent != nil {
			kVarname = n.KeyIndexIdent.Name
			eVarname = n.ValueElemIdent.Name
		}

		defer func() {
			if n.KeyIndexIdent != nil {
				state.CurrentScope()[kVarname] = nil
				state.CurrentScope()[eVarname] = nil
			}
		}()

		switch v := iteratedValue.(type) {
		case Object:
		obj_iteration:
			for k, v := range v {
				state.ctx.Take(EXECUTION_TOTAL_LIMIT_NAME, 1)

				if n.KeyIndexIdent != nil {
					state.CurrentScope()[kVarname] = k
					state.CurrentScope()[eVarname] = v
				}
				_, err := Eval(n.Body, state)
				if err != nil {
					return nil, err
				}
				if state.ReturnValue != nil {
					return nil, nil
				}

				switch state.IterationChange {
				case BreakIteration, ContinueIteration:
					state.IterationChange = NoIterationChange
					break obj_iteration
				}
			}
		case List:
		list_iteration:
			for i, e := range v {
				state.ctx.Take(EXECUTION_TOTAL_LIMIT_NAME, 1)

				if n.KeyIndexIdent != nil {

					state.CurrentScope()[kVarname] = i
					state.CurrentScope()[eVarname] = e
				}
				_, err := Eval(n.Body, state)
				if err != nil {
					return nil, err
				}
				if state.ReturnValue != nil {
					return nil, nil
				}

				switch state.IterationChange {
				case BreakIteration, ContinueIteration:
					state.IterationChange = NoIterationChange
					break list_iteration
				}
			}
		default:
			val := ToReflectVal(v)

			if val.IsValid() && val.Type().Implements(ITERABLE_INTERFACE_TYPE) {
				state.ctx.Take(EXECUTION_TOTAL_LIMIT_NAME, 1)

				iterable := val.Interface().(Iterable)
				it := iterable.Iterator()
				index := 0

			iteration:
				for it.HasNext(state.ctx) {
					state.ctx.Take(EXECUTION_TOTAL_LIMIT_NAME, 1)
					e := it.GetNext(state.ctx)

					if n.KeyIndexIdent != nil {
						state.CurrentScope()[kVarname] = index
						state.CurrentScope()[eVarname] = e
					}
					_, err := Eval(n.Body, state)
					if err != nil {
						return nil, err
					}
					if state.ReturnValue != nil {
						return nil, nil
					}
					switch state.IterationChange {
					case BreakIteration, ContinueIteration:
						state.IterationChange = NoIterationChange
						break iteration
					}
					index++
				}
				break
			}

			return nil, fmt.Errorf("cannot iterate %#v", v)
		}
		return nil, nil
	case *SwitchStatement:
		discriminant, err := Eval(n.Discriminant, state)
		if err != nil {
			return nil, err
		}
		for _, switchCase := range n.Cases {
			val, err := Eval(switchCase.Value, state)
			if err != nil {
				return nil, err
			}
			if discriminant == val {
				_, err := Eval(switchCase.Block, state)
				if err != nil {
					return nil, err
				}
				break
			}

		}
		return nil, nil
	case *MatchStatement:
		discriminant, err := Eval(n.Discriminant, state)
		if err != nil {
			return nil, err
		}

		for _, matchCase := range n.Cases {
			m, err := Eval(matchCase.Value, state)
			if err != nil {
				return nil, err
			}

			if matcher, ok := m.(Matcher); ok {
				groupMatcher, isGroupMatcher := matcher.(GroupMatcher)
				if isGroupMatcher {
					ok, groups := groupMatcher.MatchGroups(discriminant)
					if ok {
						scope := state.CurrentScope()

						for name, value := range groups {
							_, isAlreadyDefined := scope[name]
							if isAlreadyDefined {
								return nil, errors.New("match statement: group matching: cannot define twice a variable named after a group")
							}
							scope[name] = value
						}

						_, err := Eval(matchCase.Block, state)
						if err != nil {
							return nil, err
						}
						break
					}

				} else if matcher.Test(discriminant) {
					_, err := Eval(matchCase.Block, state)
					if err != nil {
						return nil, err
					}
					break
				}
			} else if reflect.TypeOf(m) == reflect.TypeOf(discriminant) { //TODO: change
				if m == discriminant {
					_, err := Eval(matchCase.Block, state)
					if err != nil {
						return nil, err
					}
					break
				} else {
					continue
				}
			} else {
				return nil, fmt.Errorf("match statement: value of type %T does not implement Matcher interface nor has the same type as value as the discriminant", m)
			}

		}
		return nil, nil
	case *BinaryExpression:

		left, err := Eval(n.Left, state)
		if err != nil {
			return nil, err
		}

		right, err := Eval(n.Right, state)
		if err != nil {
			return nil, err
		}

		switch n.Operator {
		case Add:
			return left.(int) + right.(int), nil
		case AddF:
			return left.(float64) + right.(float64), nil
		case Sub:
			return left.(int) - right.(int), nil
		case SubF:
			return left.(float64) - right.(float64), nil
		case Mul:
			return left.(int) * right.(int), nil
		case MulF:
			return left.(float64) * right.(float64), nil
		case Div:
			return left.(int) / right.(int), nil
		case DivF:
			return left.(float64) / right.(float64), nil
		case GreaterThan:
			return left.(int) > right.(int), nil
		case GreaterOrEqual:
			return left.(int) >= right.(int), nil
		case LessThan:
			return left.(int) < right.(int), nil
		case LessOrEqual:
			return left.(int) <= right.(int), nil
		case Equal:
			defer func() {
				//uncomparable
				if v := recover(); v != nil {
					result = false
					err = nil
				} else {
					panic(v)
				}
			}()
			return left == right, nil
		case NotEqual:
			defer func() {
				//uncomparable
				if v := recover(); v != nil {
					result = true
					err = nil
				} else {
					panic(v)
				}
			}()
			return left != right, nil
		case In:
			switch rightVal := right.(type) {
			case List:
				for _, e := range rightVal {
					if left == e {
						return true, nil
					}
				}
			case Object:
				for _, v := range rightVal {
					if left == v {
						return true, nil
					}
				}
			default:
				return nil, fmt.Errorf("invalid binary expression: cannot check if value is inside a %T", rightVal)
			}
			return false, nil
		case NotIn:
			switch rightVal := right.(type) {
			case List:
				for _, e := range rightVal {
					if left == e {
						return false, nil
					}
				}
			case Object:
				for _, v := range rightVal {
					if left == v {
						return false, nil
					}
				}
			default:
				return nil, fmt.Errorf("invalid binary expression: cannot check if value is inside a %T", rightVal)
			}
			return true, nil
		case Keyof:
			key, ok := left.(string)
			if !ok {
				return nil, fmt.Errorf("invalid binary expression: keyof: left operand is not a string, but a %T", left)
			}

			switch rightVal := right.(type) {
			case Object:
				_, ok := rightVal[key]
				return ok, nil
			default:
				return nil, fmt.Errorf("invalid binary expression: cannot check if non object has a key: %T", rightVal)
			}
		case Range, ExclEndRange:
			return ToReflectVal(IntRange{
				inclusiveEnd: n.Operator == Range,
				Start:        left.(int),
				End:          right.(int),
				Step:         1,
			}), nil
		case And:
			return left.(bool) && right.(bool), nil
		case Or:
			return left.(bool) || right.(bool), nil
		case Match, NotMatch:
			ok := right.(Matcher).Test(left)
			if n.Operator == NotMatch {
				ok = !ok
			}
			return ok, nil
		case Substrof:
			leftVal := ToReflectVal(left)
			rightVal := ToReflectVal(right)

			l := ""
			r := ""

			if leftVal.Kind() == reflect.String {
				l = leftVal.String()
			}

			if rightVal.Kind() == reflect.String {
				r = rightVal.String()
			}

			if leftVal.Type() == UINT8_SLICE_TYPE {
				l = string(leftVal.Interface().([]uint8))
			}

			if rightVal.Type() == UINT8_SLICE_TYPE {
				r = string(rightVal.Interface().([]uint8))
			}

			return strings.Contains(r, l), nil
		default:
			return nil, errors.New("invalid binary operator " + strconv.Itoa(int(n.Operator)))
		}
	case *UpperBoundRangeExpression:

		upperBound, err := Eval(n.UpperBound, state)
		if err != nil {
			return nil, err
		}

		switch v := upperBound.(type) {
		case int:
			return ToReflectVal(IntRange{
				unknownStart: true,
				inclusiveEnd: true,
				End:          v,
				Step:         1,
			}), nil
		case float64:
			return nil, fmt.Errorf("floating point ranges not supported")
		default:
			return ToReflectVal(QuantityRange{
				unknownStart: true,
				inclusiveEnd: true,
				End:          UnwrapReflectVal(v),
			}), nil
		}
	case *IntegerRangeLiteral:
		return &IntRange{
			unknownStart: false,
			inclusiveEnd: true,
			Start:        n.LowerBound.Value,
			End:          n.UpperBound.Value,
			Step:         1,
		}, nil
	case *RuneRangeExpression:
		return ValOf(RuneRange{
			Start: n.Lower.Value,
			End:   n.Upper.Value,
		}), nil

	case *FunctionExpression:
		return Func(n), nil
	case *LazyExpression:
		return n, nil
	case *FunctionDeclaration:
		funcName := n.Name.Name
		state.GlobalScope()[funcName] = Func(n)

		return nil, nil
	case *MemberExpression:
		left, err := Eval(n.Left, state)
		if err != nil {
			return nil, err
		}

		res, _, err := Memb(left, n.PropertyName.Name)
		return res, err
	case *ExtractionExpression:
		left, err := Eval(n.Object, state)
		if err != nil {
			return nil, err
		}
		result := Object{}

		for _, key := range n.Keys.Keys {
			prop, _, err := Memb(left, key.Name)
			if err != nil {
				return nil, err
			}
			result[key.Name] = prop
		}
		return result, nil
	case *IndexExpression:
		list, err := Eval(n.Indexed, state)
		if err != nil {
			return nil, err
		}

		index, err := Eval(n.Index, state)
		if err != nil {
			return nil, err
		}

		return list.(List)[index.(int)], nil
	case *SliceExpression:
		slice, err := Eval(n.Indexed, state)
		if err != nil {
			return nil, err
		}

		l := slice.(List)
		var startIndex interface{} = 0
		if n.StartIndex != nil {
			startIndex, err = Eval(n.StartIndex, state)
			if err != nil {
				return nil, err
			}
		}

		var endIndex interface{} = math.MaxInt
		if n.EndIndex != nil {
			endIndex, err = Eval(n.EndIndex, state)
			if err != nil {
				return nil, err
			}
		}

		start := startIndex.(int)
		if start > len(l) {
			start = len(l)
		}
		end := endIndex.(int)
		if end > len(l) {
			end = len(l)
		}

		return GetSlice(slice, start, end)
	case *KeyListExpression:
		list := KeyList{}

		for _, key := range n.Keys {
			list = append(list, string(key.Name))
		}

		return list, nil
	case *BooleanConversionExpression:
		valueToConvert, err := Eval(n.Expr, state)
		if err != nil {
			return nil, err
		}

		return toBool(ToReflectVal(valueToConvert)), nil
	case *PatternIdentifierLiteral:
		//should we return an error if not present
		return state.ctx.resolveNamedPattern(n.Name), nil
	case *PatternDefinition:
		right, err := CompilePatternNode(n.Right, state)
		if err != nil {
			return nil, err
		}

		pattern, ok := right.(Matcher)
		if !ok {
			return nil, errors.New("pattern definition failed, value should implement the Matcher interface")
		}

		state.ctx.addNamedPattern(n.Left.Name, pattern)
		return nil, nil
	case *PatternPiece:
		if n.Kind != StringPattern {
			return nil, errors.New("evaluation of non-string pattern pieces not implemented yet")
		}

		return CompileStringPatternNode(n, state)
	case *PatternUnion:
		return CompileStringPatternNode(n, state)
	case *ObjectPatternLiteral:
		pattern := &ObjectPattern{
			EntryMatchers: make(map[string]Matcher),
		}
		for _, p := range n.Properties {
			name := p.Name()
			value, err := Eval(p.Value, state)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate object pattern literal, error when evaluating value for '%s': %s", name, err.Error())
			}

			switch m := value.(type) {
			case Matcher:
				pattern.EntryMatchers[name] = m
			default:
				if IsSimpleGopherVal(m) {
					pattern.EntryMatchers[name] = ExactSimpleValueMatcher{m}
				} else {
					return nil, fmt.Errorf("failed to evaluate object pattern literal, matcher for key '%s' is not a matcher or a simple value but a %T", name, value)
				}
			}
		}

		return pattern, nil
	case *ListPatternLiteral:
		pattern := &ListPattern{
			ElementMatchers: []Matcher{},
		}
		for _, e := range n.Elements {
			value, err := Eval(e, state)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate list pattern literal, error when evaluating an element: %s", err.Error())
			}

			switch m := value.(type) {
			case Matcher:
				pattern.ElementMatchers = append(pattern.ElementMatchers, m)
			default:
				if IsSimpleGopherVal(m) {
					pattern.ElementMatchers = append(pattern.ElementMatchers, ExactSimpleValueMatcher{m})
				} else {
					return nil, fmt.Errorf("failed to evaluate list pattern literal, matcher for an alement is not a matcher or a simple value but a %T", value)
				}
			}
		}

		return pattern, nil
	default:
		return nil, fmt.Errorf("cannot evaluate %#v (%T)\n%s", node, node, debug.Stack())
	}

}

//========== permissions ==========

type StackPermission struct {
	maxHeight int
}

func (perm StackPermission) Kind() PermissionKind {
	return UsePerm
}

func (perm StackPermission) Includes(otherPerm Permission) bool {
	otherStackPerm, ok := otherPerm.(StackPermission)
	if !ok {
		return false
	}

	return perm.includes(otherStackPerm)
}

func (perm StackPermission) includes(otherPerm StackPermission) bool {
	return otherPerm.maxHeight <= perm.maxHeight
}

func (perm StackPermission) String() string {
	return fmt.Sprintf("[max-stack-height %d]", perm.maxHeight)
}

type GlobalVarPermission struct {
	Kind_ PermissionKind
	Name  string //"*" means any
}

func (perm GlobalVarPermission) Kind() PermissionKind {
	return perm.Kind_
}

func (perm GlobalVarPermission) Includes(otherPerm Permission) bool {
	otherGlobVarPerm, ok := otherPerm.(GlobalVarPermission)
	if !ok || perm.Kind() != otherGlobVarPerm.Kind() {
		return false
	}

	return perm.Name == "*" || perm.Name == otherGlobVarPerm.Name
}

func (perm GlobalVarPermission) String() string {
	return fmt.Sprintf("[%s global(s) '%s']", perm.Kind_, perm.Name)
}

type RoutinePermission struct {
	Kind_ PermissionKind
}

func (perm RoutinePermission) Kind() PermissionKind {
	return perm.Kind_
}

func (perm RoutinePermission) Includes(otherPerm Permission) bool {
	otherRoutinePerm, ok := otherPerm.(RoutinePermission)

	return ok && perm.Kind_ == otherRoutinePerm.Kind_
}

func (perm RoutinePermission) String() string {
	return fmt.Sprintf("[%s routine]", perm.Kind_)
}

type FilesystemPermission struct {
	Kind_  PermissionKind
	Entity interface{} //Path, PathPattern ...
}

func (perm FilesystemPermission) Kind() PermissionKind {
	return perm.Kind_
}

func (perm FilesystemPermission) Includes(otherPerm Permission) bool {
	otherFsPerm, ok := otherPerm.(FilesystemPermission)
	if !ok || perm.Kind() != otherFsPerm.Kind() {
		return false
	}

	switch e := perm.Entity.(type) {
	case Path:
		otherPath, ok := otherFsPerm.Entity.(Path)
		return ok && e == otherPath
	case PathPattern:
		return e.Test(otherFsPerm.Entity)
	}

	return false
}

func (perm FilesystemPermission) String() string {
	return fmt.Sprintf("[%s path(s) %s]", perm.Kind_, perm.Entity)
}

type CommandPermission struct {
	CommandName         string
	SubcommandNameChain []string //can be empty
}

func (perm CommandPermission) Kind() PermissionKind {
	return UsePerm
}

func (perm CommandPermission) Includes(otherPerm Permission) bool {

	otherCmdPerm, ok := otherPerm.(CommandPermission)
	if !ok || perm.Kind() != otherCmdPerm.Kind() {
		return false
	}

	if otherCmdPerm.CommandName != perm.CommandName || len(otherCmdPerm.SubcommandNameChain) != len(perm.SubcommandNameChain) {
		return false
	}

	for i, name := range perm.SubcommandNameChain {
		if otherCmdPerm.SubcommandNameChain[i] != name {
			return false
		}
	}

	return true
}

func (perm CommandPermission) String() string {
	b := bytes.NewBufferString("[exec command:")
	b.WriteString(perm.CommandName)

	for _, name := range perm.SubcommandNameChain {
		b.WriteString(" ")
		b.WriteString(name)
	}
	b.WriteString("]")

	return b.String()
}

type HttpPermission struct {
	Kind_  PermissionKind
	Entity interface{} //URL, URLPattern, HTTPHost, HTTPHostPattern ....
}

func (perm HttpPermission) Kind() PermissionKind {
	return perm.Kind_
}

func (perm HttpPermission) Includes(otherPerm Permission) bool {
	otherHttpPerm, ok := otherPerm.(HttpPermission)
	if !ok || perm.Kind() != otherHttpPerm.Kind() {
		return false
	}

	switch e := perm.Entity.(type) {
	case URL:
		otherURL, ok := otherHttpPerm.Entity.(URL)
		parsedURL, _ := url.Parse(string(e))

		if parsedURL.RawQuery == "" {
			parsedURL.ForceQuery = false

			otherParsedURL, _ := url.Parse(string(otherURL))
			otherParsedURL.RawQuery = ""
			otherParsedURL.ForceQuery = false

			return parsedURL.String() == otherParsedURL.String()
		}

		return ok && e == otherURL
	case URLPattern:
		return e.Test(otherHttpPerm.Entity)
	case HTTPHost:
		host := strings.ReplaceAll(string(e), "https://", "")
		switch other := otherHttpPerm.Entity.(type) {
		case URL:
			otherURL, err := url.Parse(string(other))
			if err != nil {
				return false
			}
			return otherURL.Host == host
		case HTTPHost:
			return e == other
		}
	case HTTPHostPattern:
		return e.Test(otherHttpPerm.Entity)
	}

	return false
}

func (perm HttpPermission) String() string {
	return fmt.Sprintf("[%s %s]", perm.Kind_, perm.Entity)
}

type ContextlessCallPermission struct {
	FuncMethodName   string
	ReceiverTypeName string
}

func (perm ContextlessCallPermission) Kind() PermissionKind {
	return UsePerm
}

func (perm ContextlessCallPermission) Includes(otherPerm Permission) bool {

	otherCallPerm, ok := otherPerm.(ContextlessCallPermission)
	if !ok || perm.Kind() != otherCallPerm.Kind() {
		return false
	}

	return otherCallPerm.ReceiverTypeName == perm.ReceiverTypeName && otherCallPerm.FuncMethodName == perm.FuncMethodName
}

func (perm ContextlessCallPermission) String() string {
	b := bytes.NewBufferString("[call contextless: ")

	if perm.ReceiverTypeName != "" {
		b.WriteString(perm.ReceiverTypeName + ".")
	}

	b.WriteString(perm.FuncMethodName)
	b.WriteString("]")

	return b.String()
}

type Iterable interface {
	Iterator() Iterator
}

type Iterator interface {
	HasNext(*Context) bool
	GetNext(*Context) interface{}
}

type IntRange struct {
	unknownStart bool
	inclusiveEnd bool
	Start        int
	End          int
	Step         int
}

func (r IntRange) Iterator() Iterator {
	if r.unknownStart {
		log.Panicln("cannot create iterator from an IntRange with no known start")
	}
	return &IntRangeIterator{
		range_: r,
		next:   r.Start,
	}
}

func (r IntRange) Random() interface{} {
	if r.unknownStart {
		panic("Random() not supported for int ranges with no start")
	}
	start := r.Start
	end := r.End

	if !r.inclusiveEnd {
		end = r.End - 1
	}

	return start + rand.Intn(end-start+1)
}

type IntRangeIterator struct {
	range_ IntRange
	next   int
}

func (it IntRangeIterator) HasNext(*Context) bool {
	if it.range_.inclusiveEnd {
		return it.next <= it.range_.End
	}
	return it.next < it.range_.End
}

func (it *IntRangeIterator) GetNext(ctx *Context) interface{} {
	if !it.HasNext(ctx) {
		log.Panicln("no next value in int range iterator")
	}

	v := it.next
	it.next += 1
	return v
}

type QuantityRange struct {
	unknownStart bool
	inclusiveEnd bool
	Start        interface{}
	End          interface{}
}

//TODO: implement Iterable
type RuneRange struct {
	Start rune
	End   rune
}

func (r RuneRange) RandomRune() rune {
	offset := rand.Intn(int(r.End - r.Start + 1))
	return r.Start + rune(offset)
}

func (r RuneRange) Random() interface{} {
	return r.RandomRune()
}

type ByteCount int
type LineCount int
type ByteRate int
type SimpleRate int

//LIMITATIONS

//Token bucket implementation, see https://github.com/DavidCai1993/token-bucket

// TokenBucket represents a token bucket
// (https://en.wikipedia.org/wiki/Token_bucket) which based on multi goroutines,
// and is safe to use under concurrency environments.
type TokenBucket struct {
	interval          time.Duration
	ticker            *time.Ticker
	tokenMutex        *sync.Mutex
	waitingQuqueMutex *sync.Mutex
	waitingQuque      *list.List
	cap               int64
	avail             int64
	increment         int64
	decrementFn       func(time.Time) int64
	lastDecrementTime time.Time
}

type waitingJob struct {
	ch        chan struct{}
	need      int64
	use       int64
	abandoned bool
}

// newBucket returns a new token bucket with specified fill interval and
// capability. The bucket is initially full.
func newBucket(interval time.Duration, cap int64, inc int64, decrementFn func(time.Time) int64) *TokenBucket {
	if interval < 0 {
		panic(fmt.Sprintf("ratelimit: interval %v should > 0", interval))
	}

	if cap < 0 {
		panic(fmt.Sprintf("ratelimit: capability %v should > 0", cap))
	}

	tb := &TokenBucket{
		interval:          interval,
		tokenMutex:        &sync.Mutex{},
		waitingQuqueMutex: &sync.Mutex{},
		waitingQuque:      list.New(),
		cap:               cap,
		avail:             cap,
		increment:         inc,
		ticker:            time.NewTicker(interval),
		decrementFn:       decrementFn,
		lastDecrementTime: time.Now(),
	}

	go tb.adjustDaemon()

	return tb
}

// Capability returns the capability of this token bucket.
func (tb *TokenBucket) Capability() int64 {
	return tb.cap
}

// Availible returns how many tokens are availible in the bucket.
func (tb *TokenBucket) Availible() int64 {
	tb.tokenMutex.Lock()
	defer tb.tokenMutex.Unlock()

	return tb.avail
}

// TryTake trys to task specified count tokens from the bucket. if there are
// not enough tokens in the bucket, it will return false.
func (tb *TokenBucket) TryTake(count int64) bool {
	return tb.tryTake(count, count)
}

// Take tasks specified count tokens from the bucket, if there are
// not enough tokens in the bucket, it will keep waiting until count tokens are
// availible and then take them.
func (tb *TokenBucket) Take(count int64) {
	tb.waitAndTake(count, count)
}

// TakeMaxDuration tasks specified count tokens from the bucket, if there are
// not enough tokens in the bucket, it will keep waiting until count tokens are
// availible and then take them or just return false when reach the given max
// duration.
func (tb *TokenBucket) TakeMaxDuration(count int64, max time.Duration) bool {
	return tb.waitAndTakeMaxDuration(count, count, max)
}

// Wait will keep waiting until count tokens are availible in the bucket.
func (tb *TokenBucket) Wait(count int64) {
	tb.waitAndTake(count, 0)
}

// WaitMaxDuration will keep waiting until count tokens are availible in the
// bucket or just return false when reach the given max duration.
func (tb *TokenBucket) WaitMaxDuration(count int64, max time.Duration) bool {
	return tb.waitAndTakeMaxDuration(count, 0, max)
}

func (tb *TokenBucket) tryTake(need, use int64) bool {
	tb.checkCount(use)

	tb.tokenMutex.Lock()
	defer tb.tokenMutex.Unlock()

	if need <= tb.avail {
		tb.avail -= use

		return true
	}

	return false
}

func (tb *TokenBucket) waitAndTake(need, use int64) {
	if ok := tb.tryTake(need, use); ok {
		return
	}

	w := &waitingJob{
		ch:   make(chan struct{}),
		use:  use,
		need: need,
	}

	tb.addWaitingJob(w)

	<-w.ch
	tb.avail -= use
	w.ch <- struct{}{}

	close(w.ch)
}

func (tb *TokenBucket) waitAndTakeMaxDuration(need, use int64, max time.Duration) bool {
	if ok := tb.tryTake(need, use); ok {
		return true
	}

	w := &waitingJob{
		ch:   make(chan struct{}),
		use:  use,
		need: need,
	}

	defer close(w.ch)

	tb.addWaitingJob(w)

	select {
	case <-w.ch:
		tb.avail -= use
		w.ch <- struct{}{}
		return true
	case <-time.After(max):
		w.abandoned = true
		return false
	}
}

// Destroy destroys the token bucket and stop the inner channels.
func (tb *TokenBucket) Destroy() {
	tb.ticker.Stop()
}

func (tb *TokenBucket) adjustDaemon() {
	var waitingJobNow *waitingJob

	for range tb.ticker.C {

		tb.tokenMutex.Lock()

		if tb.avail < tb.cap {
			tb.avail = max64(0, tb.avail+tb.increment)
		}

		if tb.decrementFn != nil {
			tb.avail = max64(0, tb.avail-tb.decrementFn(tb.lastDecrementTime))
		}

		tb.lastDecrementTime = time.Now()
		element := tb.getFrontWaitingJob()

		if element != nil {
			if waitingJobNow == nil || waitingJobNow.abandoned {
				waitingJobNow = element.Value.(*waitingJob)

				tb.removeWaitingJob(element)
			}
		}

		if waitingJobNow != nil && tb.avail >= waitingJobNow.need && !waitingJobNow.abandoned {
			waitingJobNow.ch <- struct{}{}

			<-waitingJobNow.ch

			waitingJobNow = nil
		}

		tb.tokenMutex.Unlock()
	}
}

func (tb *TokenBucket) addWaitingJob(w *waitingJob) {
	tb.waitingQuqueMutex.Lock()
	tb.waitingQuque.PushBack(w)
	tb.waitingQuqueMutex.Unlock()
}

func (tb *TokenBucket) getFrontWaitingJob() *list.Element {
	tb.waitingQuqueMutex.Lock()
	e := tb.waitingQuque.Front()
	tb.waitingQuqueMutex.Unlock()

	return e
}

func (tb *TokenBucket) removeWaitingJob(e *list.Element) {
	tb.waitingQuqueMutex.Lock()
	tb.waitingQuque.Remove(e)
	tb.waitingQuqueMutex.Unlock()
}

func (tb *TokenBucket) checkCount(count int64) {
	if count < 0 || count > tb.cap {
		panic(fmt.Sprintf("token-bucket: count %v should be less than bucket's"+
			" capablity %v", count, tb.cap))
	}
}

//END OF TOCKEN BUCKET IMPLEMENTATION
