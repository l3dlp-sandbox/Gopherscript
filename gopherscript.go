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
const HTTP_URL_PATTERN = "^https?:\\/\\/(localhost|(www\\.)?[-a-zA-Z0-9@:%._+~#=]{1,32}\\.[a-zA-Z0-9]{1,6})\\b([-a-zA-Z0-9@:%_+.~#?&//=]{0,100})$"
const LOOSE_HTTP_EXPR_PATTERN = "^https?:\\/\\/(localhost|(www\\.)?[-a-zA-Z0-9@:%._+~#=]{1,32}\\.[a-zA-Z0-9]{1,6})\\b([-a-zA-Z0-9@:%_+.~#?&//=$]{0,100})$"
const LOOSE_HTTP_HOST_PATTERN_PATTERN = "^https?:\\/\\/(\\*|(www\\.)?[-a-zA-Z0-9.*]{1,32}\\.[a-zA-Z0-9*]{1,6})(:[0-9]{1,5})?$"
const IMPLICIT_KEY_LEN_KEY = "__len"
const GOPHERSCRIPT_MIMETYPE = "application/gopherscript"
const RETURN_1_MODULE_HASH = "SG2a/7YNuwBjsD2OI6bM9jZM4gPcOp9W8g51DrQeyt4="
const RETURN_GLOBAL_A_MODULE_HASH = "UYvV2gLwfuQ2D91v7PzQ8RMugUTcM0lOysCMqMqXfmg"
const TOKEN_BUCKET_INTERVAL = 10 * time.Millisecond

var HTTP_URL_REGEX = regexp.MustCompile(HTTP_URL_PATTERN)
var LOOSE_HTTP_HOST_PATTERN_REGEX = regexp.MustCompile(LOOSE_HTTP_HOST_PATTERN_PATTERN)
var LOOSE_HTTP_EXPR_PATTERN_REGEX = regexp.MustCompile(LOOSE_HTTP_EXPR_PATTERN)
var isSpace = regexp.MustCompile(`^\s+`).MatchString
var KEYWORDS = []string{"if", "else", "require", "for", "assign", "const", "fn", "switch", "match", "import", "sr", "return", "break", "continue"}
var PERMISSION_KIND_STRINGS = []string{"read", "update", "create", "delete", "use", "consume", "provide"}

var CTX_PTR_TYPE = reflect.TypeOf(&Context{})
var ERROR_INTERFACE_TYPE = reflect.TypeOf((*error)(nil)).Elem()
var ITERABLE_INTERFACE_TYPE = reflect.TypeOf((*Iterable)(nil)).Elem()
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

func isDelim(r rune) bool {
	switch r {
	case '{', '}', '[', ']', '(', ')', ',', ';', ':':
		return true
	default:
		return false
	}
}

func isNotPairedOrIsClosingDelim(r rune) bool {
	switch r {
	case ',', ';', ':', ')', ']', '}':
		return true
	default:
		return false
	}
}

func min(a, b int) int {
	if a < b {
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
}

type Statement interface {
	Node
}

type NodeSpan struct {
	Start int
	End   int
}

type NodeBase struct {
	Span NodeSpan
}

func (base NodeBase) Base() NodeBase {
	return base
}

func (base NodeBase) IncludedIn(node Node) bool {
	return base.Span.Start >= node.Base().Span.Start && base.Span.End <= node.Base().Span.End
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
	Left          Node
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

type BooleanLiteral struct {
	NodeBase
	Value bool
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

type RelativePathExpression struct {
	NodeBase
	Slices []Node
}

type AbsolutePathExpression struct {
	NodeBase
	Slices []Node
}

type URLExpression struct {
	NodeBase
	Raw      string
	HostPart string
	Path     *AbsolutePathExpression
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
	Properties []ObjectProperty
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

func (objLit ObjectLiteral) PermissionsLimitations(
	globalConsts *GlobalConstantDeclarations,
	runningState *State,
	handleCustomType func(kind PermissionKind, name string) ([]Permission, bool, error),
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
		for _, nameValueNodes := range globalConsts.NamesValues {
			globalScope[nameValueNodes[0].(*IdentifierLiteral).Name] = MustEval(nameValueNodes[1], nil)
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

			for _, limitProp := range limitObjLiteral.Properties {

				switch node := limitProp.Value.(type) {
				case *RateLiteral:
					limitation := Limitation{
						Name: limitProp.Name(),
						Rate: MustEval(node, state).(ByteRate),
					}
					limitations = append(limitations, limitation)
				default:
					log.Panicln("invalid requirements, limits: only byte rate literals are supported for now.")
				}
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
							customPerms, handled, err := handleCustomType(permKind, typeName)
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
			if !isSimpleValueLiteral(n) {
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

type GlobalConstantDeclarations struct {
	NodeBase
	NamesValues [][2]Node
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
)

var BINARY_OPERATOR_STRINGS = []string{
	"+", "+.", "-", "-.", "*", "*.", "/", "/.", "++", "<", "<.", "<=", "<=", ">", ">.", ">=", ">=.", "==", "!=",
	"in", "not-in", "keyof", ".", "..", "..<", "and", "or",
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

type UpperBoundRangeExpression struct {
	NodeBase
	UpperBound Node
}

type FunctionExpression struct {
	NodeBase
	Parameters   []FunctionParameter
	Body         *Block
	Requirements *Requirements
}

type FunctionDeclaration struct {
	NodeBase
	Function *FunctionExpression
	Name     *IdentifierLiteral
}

type FunctionParameter struct {
	Var *IdentifierLiteral
}

type Requirements struct {
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

func isSimpleValueLiteral(node Node) bool {
	switch node.(type) {
	case *StringLiteral, *IdentifierLiteral, *IntLiteral, *FloatLiteral, *AbsolutePathLiteral, *AbsolutePathPatternLiteral, *RelativePathLiteral,
		*RelativePathPatternLiteral, *BooleanLiteral, *NilLiteral, *HTTPHostLiteral, *HTTPHostPatternLiteral, *URLLiteral, *URLPatternLiteral:
		return true
	default:
		return false
	}
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

func samePointer(a, b interface{}) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func CallFunc(calleeNode Node, state *State, arguments interface{}, must bool) (interface{}, error) {
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
		name := c.Left.(*IdentifierLiteral).Name
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
			v, optReceiverType, err = memb(v, idents.Name)
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
		callee, optReceiverType, err = memb(left, c.PropertyName.Name)
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

func ParseModule(str string, fpath string) (result *Module, resultErr error) {
	s := []rune(str)

	defer func() {
		v := recover()
		if err, ok := v.(error); ok {
			resultErr = err
		}

		//add location in error message
		if parsingErr, ok := resultErr.(ParsingError); ok {
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

			parsingErr.Message = fmt.Sprintf("%s:%d:%d: %s: %s", fpath, line, col, parsingErr.Message, debug.Stack())
			resultErr = parsingErr
		} else if resultErr != nil {
			resultErr = fmt.Errorf("%s: %s", resultErr.Error(), debug.Stack())
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

	eatComment := func() {
		if i < len(s)-1 && (s[i+1] == ' ' || s[i+1] == '\t') {
			i += 2
			for i < len(s) && s[i] != '\n' {
				i++
			}
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
				eatComment()
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
				eatComment()
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
				eatComment()
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
	var parseExpression func() Node
	var parseStatement func() Statement
	var parseGlobalConstantDeclarations func() *GlobalConstantDeclarations
	var parseRequirements func() *Requirements
	var parseFunction func(int) Node

	parseBlock = func() *Block {
		openingBraceIndex := i
		i++

		var stmts []Node

		for i < len(s) && s[i] != '}' {
			eatSpaceNewLineSemiColonComment()

			if i < len(s) && s[i] == '}' {
				break
			}

			stmts = append(stmts, parseStatement())
			eatSpaceNewLineSemiColonComment()
		}

		if i >= len(s) {
			panic(ParsingError{
				"unterminated block, missing closing brace '}",
				i,
				openingBraceIndex,
				KnownType,
				(*Block)(nil),
			})
		}

		if s[i] != '}' {
			panic(ParsingError{
				"invalid block",
				i,
				openingBraceIndex,
				KnownType,
				(*Block)(nil),
			})
		}
		i++

		end := i
		mod.Statements = stmts

		return &Block{
			NodeBase: NodeBase{
				Span: NodeSpan{openingBraceIndex, end},
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

	parsePathExpressionSlices := func(start int, end int) []Node {
		slices := make([]Node, 0)
		index := start
		sliceStart := start
		inInterpolation := false

		for index < end {

			if inInterpolation {
				if s[index] == '$' {
					name := string(s[sliceStart+1 : index])

					slices = append(slices, &Variable{
						NodeBase: NodeBase{
							NodeSpan{sliceStart, index + 1},
						},
						Name: name,
					})
					inInterpolation = false
					sliceStart = index + 1
				} else if !isIdentChar(s[index]) {
					panic(ParsingError{
						"a path interpolation should contain an identifier without spaces, example: $name$ ",
						i,
						-1,
						UnspecifiedCategory,
						nil,
					})
				}

			} else if s[index] == '$' {
				slice := string(s[sliceStart:index]) //previous cannot be an interpolation

				slices = append(slices, &PathSlice{
					NodeBase: NodeBase{
						NodeSpan{sliceStart, index},
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
				},
				Value: string(s[sliceStart:index]),
			})
		}
		return slices
	}

	parsePathLikeExpression := func() Node {
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
			if (r == '[' || r == '*' || r == '?') && countPrevBackslashes()%2 == 0 {
				if strings.Contains(value, "$") {
					panic(ParsingError{
						"a path pattern cannot be interpolated '" + value + "'",
						i,
						start,
						Pathlike,
						nil,
					})
				}

				if strings.HasSuffix(value, "/...") {
					panic(ParsingError{
						"prefix path patterns cannot contain globbing patterns '" + value + "'",
						i,
						start,
						Pathlike,
						nil,
					})
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

			if strings.Contains(value, "$$") {
				panic(ParsingError{
					"a path expression cannot contain interpolations next to each others",
					i,
					start,
					Pathlike,
					nil,
				})
			}

			slices := parsePathExpressionSlices(start, i)

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
			if !strings.HasSuffix(value, "/...") {
				panic(ParsingError{
					"'/...' can only be present at the end of a path pattern  '" + value + "'",
					i,
					start,
					Pathlike,
					nil,
				})
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

	parseURLLike := func(start int, ident *IdentifierLiteral) Node {
		i += 3
		for i < len(s) && !isSpace(string(s[i])) && (!isDelim(s[i]) || s[i] == ':') {
			i++
		}

		_url := string(s[start:i])
		isPrefixPattern := strings.HasSuffix(_url, "/...")

		//TODO: think about escaping in URLs with '\': specs, server implementations

		if strings.Contains(_url, "..") && (!isPrefixPattern || strings.Count(_url, "..") != 1) {
			panic(ParsingError{
				"URL-like patterns cannot contain more than two subsequents dots except /... at the end for URL patterns",
				i,
				start,
				URLlike,
				nil,
			})
		}

		if strings.Contains(_url, "?") {
			panic(ParsingError{
				"URLs with a query parts are not supported yet'" + _url,
				i,
				start,
				URLlike,
				nil,
			})
		}
		span := NodeSpan{ident.Span.Start, i}

		if !HTTP_URL_REGEX.MatchString(_url) {

			switch {
			case LOOSE_HTTP_HOST_PATTERN_REGEX.MatchString(_url):
				pattern := _url[strings.Index(_url, "://")+3:]
				pattern = strings.Split(pattern, ":")[0]
				parts := strings.Split(pattern, ".")

				if len(parts) == 1 {
					if parts[0] != "*" {
						panic(ParsingError{
							"invalid HTTP host pattern '" + _url,
							i,
							start,
							URLlike,
							(*HTTPHostPatternLiteral)(nil),
						})
					}
				} else {
					replaced := strings.ReplaceAll(_url, "*", "com")
					if _, err := url.Parse(replaced); err != nil {

						panic(ParsingError{
							"invalid HTTP host pattern '" + _url + "' : " + err.Error(),
							i,
							start,
							URLlike,
							(*HTTPHostPatternLiteral)(nil),
						})
					}
				}

				return &HTTPHostPatternLiteral{
					NodeBase: NodeBase{
						Span: span,
					},
					Value: _url,
				}
			case LOOSE_HTTP_EXPR_PATTERN_REGEX.MatchString(_url):
				if strings.Contains(_url, "$$") {
					panic(ParsingError{
						"an URL expression cannot contain interpolations next to each others",
						i,
						start,
						URLlike,
						nil,
					})
				}

				if isPrefixPattern {
					panic(ParsingError{
						"an URL expression cannot ends with /...",
						i,
						start,
						URLlike,
						(*URLExpression)(nil),
					})
				}

				pathStart := ident.Span.End + len("://")

				for s[pathStart] != '/' {
					pathStart++
				}

				slices := parsePathExpressionSlices(pathStart, i)

				return &URLExpression{
					NodeBase: NodeBase{span},
					Raw:      _url,
					HostPart: string(s[span.Start:pathStart]),
					Path: &AbsolutePathExpression{
						NodeBase: NodeBase{
							NodeSpan{pathStart, i},
						},
						Slices: slices,
					},
				}
			}
		}

		//remove this check ?
		if !HTTP_URL_REGEX.MatchString(_url) && _url != "https://localhost" {
			panic(ParsingError{
				"invalid URL '" + _url + "'",
				i,
				start,
				URLlike,
				nil,
			})
		}

		parsed, err := url.Parse(_url)
		if err != nil {
			panic(ParsingError{
				"invalid URL '" + _url + "'",
				i,
				start,
				URLlike,
				nil,
			})
		}

		if isPrefixPattern {
			return &URLPatternLiteral{
				NodeBase: NodeBase{
					Span: span,
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

		return &HTTPHostLiteral{
			NodeBase: NodeBase{
				Span: span,
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

		if i < len(s)-1 && s[i] == '.' {
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
					panic(ParsingError{
						"unterminated identifier member expression",
						i,
						start,
						IdentLike,
						(*IdentifierMemberExpression)(nil),
					})
				}

				if !isAlpha(s[i]) && s[i] != '_' {
					panic(ParsingError{
						"property name should start with a letter not '" + string(s[i]) + "'",
						i,
						start,
						IdentLike,
						(*IdentifierMemberExpression)(nil),
					})
				}

				for i < len(s) && isAlpha(s[i]) {
					i++
				}

				propName := string(s[start:i])

				memberExpr.PropertyNames = append(memberExpr.PropertyNames, &IdentifierLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, i},
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
				return parseURLLike(start, ident)
			}
		}

		if i < len(s) && strings.HasPrefix(string(s[i:]), "://") {
			panic(ParsingError{
				"invalid URI : unsupported protocol",
				i,
				start,
				URLlike,
				nil,
			})
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
				panic(ParsingError{
					"unterminated key list, missing closing brace '}'",
					i,
					start,
					KnownType,
					(*KeyListExpression)(nil),
				})
			}

			if ident, ok := parseExpression().(*IdentifierLiteral); ok {
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

		if i >= len(s) {
			panic(ParsingError{
				"unterminated key list, missing closing brace '}'",
				i,
				start,
				KnownType,
				(*KeyListExpression)(nil),
			})
		}
		i++

		return &KeyListExpression{
			NodeBase: NodeBase{
				NodeSpan{start, i},
			},
			Keys: idents,
		}
	}

	parseExpression = func() Node {
		//these variables are only used for expressions that can be on the left of a member/slice/index/call expression
		//other expressions are directly returned
		var lhs Node
		var first Node
		var parenthesizedFirstStart int

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
		//TODO: refactor ?
		case '_', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z':
			identLike := parseIdentLike()
			spawnExprStart := identLike.Base().Span.Start
			var name string

			switch v := identLike.(type) {
			case *IdentifierLiteral:
				name = v.Name
			case *IdentifierMemberExpression:
				name = v.Left.(*IdentifierLiteral).Name
			default:
				return v
			}

			if name == "sr" {
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
				e := parseExpression()

				switch ev := e.(type) {
				case *IdentifierLiteral:
					routineGroupIdent = ev
				default:
					globals = e
				}

				eatSpace()

				if globals == nil {
					globals = parseExpression()
					eatSpace()
				}

				if i >= len(s) {
					panic(ParsingError{
						"invalid spawn expression: sr should be followed by two expressions",
						i,
						spawnExprStart,
						KnownType,
						(*SpawnExpression)(nil),
					})
				}

				var expr Node

				if s[i] == '{' {
					start := i
					i++
					emod := &EmbeddedModule{}

					var stmts []Node

					eatSpace()
					requirements := parseRequirements()

					eatSpaceNewLineSemiColonComment()

					for i < len(s) && s[i] != '}' {
						stmts = append(stmts, parseStatement())
						eatSpaceNewLineSemiColonComment()
					}

					if i >= len(s) || s[i] != '}' {
						panic(ParsingError{
							"unterminated embedded module",
							i,
							start,
							KnownType,
							(*EmbeddedModule)(nil),
						})
					}

					i++

					emod.Requirements = requirements
					emod.Statements = stmts
					emod.NodeBase = NodeBase{
						NodeSpan{start, i},
					}
					expr = emod
				} else {
					expr = parseExpression()
				}

				eatSpace()
				var grantedPermsLit *ObjectLiteral
				if i < len(s) && s[i] == 'a' {
					allowIdent := parseExpression()
					if ident, ok := allowIdent.(*IdentifierLiteral); !ok || ident.Name != "allow" {
						panic(ParsingError{
							"spawn expression: argument should be followed by a the 'allow' keyword",
							i,
							spawnExprStart,
							KnownType,
							(*SpawnExpression)(nil),
						})
					}

					eatSpace()

					grantedPerms := parseExpression()
					var ok bool
					grantedPermsLit, ok = grantedPerms.(*ObjectLiteral)
					if !ok {
						panic(ParsingError{
							"spawn expression: 'allow' keyword should be followed by an object literal (permissions)",
							i,
							spawnExprStart,
							KnownType,
							(*SpawnExpression)(nil),
						})
					}
				}

				return &SpawnExpression{
					NodeBase: NodeBase{
						NodeSpan{identLike.Base().Span.Start, i},
					},
					GroupIdent:         routineGroupIdent,
					Globals:            globals,
					ExprOrVar:          expr,
					GrantedPermissions: grantedPermsLit,
				}
			}

			if name == "fn" {
				return parseFunction(identLike.Base().Span.Start)
			}

			if i >= len(s) {
				return identLike
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

				str := parseExpression()
				call.Arguments = append(call.Arguments, str)
				call.NodeBase.Span.End = str.Base().Span.End
				return call
			case s[i] == '(' && !isKeyword(name): //func_name(...
				i++
				eatSpace()

				call := &Call{
					NodeBase: NodeBase{
						NodeSpan{identLike.Base().Span.Start, 0},
					},
					Callee:    identLike,
					Arguments: nil,
				}

				for i < len(s) && s[i] != ')' {
					eatSpaceNewlineComma()
					arg := parseExpression()

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

				return call
			case s[i] == '$': //funcname$ ...
				i++
				if i >= len(s) || (s[i] != '\t' && s[i] != ' ') {
					panic(ParsingError{
						"a non-parenthesized call expression should have arguments and the callee (<name>$) should be followed by a space",
						i,
						identLike.Base().Span.Start,
						KnownType,
						(*Call)(nil),
					})
				}

				call := &Call{
					NodeBase: NodeBase{
						Span: NodeSpan{identLike.Base().Span.Start, 0},
					},
					Callee:    identLike,
					Arguments: nil,
					Must:      true,
				}

				for i < len(s) && s[i] != '\n' && !isNotPairedOrIsClosingDelim(s[i]) {
					eatSpaceAndComments()

					if s[i] == '\n' || isNotPairedOrIsClosingDelim(s[i]) {
						break
					}

					arg := parseExpression()

					call.Arguments = append(call.Arguments, arg)
					eatSpaceAndComments()
				}

				if i < len(s) && s[i] == '\n' {
					i++
				}

				call.NodeBase.Span.End = call.Arguments[len(call.Arguments)-1].Base().Span.End
				return call
			}

			return identLike
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9': //integers and floating point numbers
			start := i
			for i < len(s) && (isDigit(s[i]) || s[i] == '.' || s[i] == '-') {
				i++
			}

			raw := string(s[start:i])

			var literal Node
			var fValue float64

			if strings.Contains(raw, ".") {
				float, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					panic(ParsingError{
						"invalid floating point literal '" + raw + "'",
						i,
						start,
						KnownType,
						(*FloatLiteral)(nil),
					})
				}
				literal = &FloatLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, i},
					},
					Raw:   raw,
					Value: float,
				}

				fValue = float
			} else {

				integer, err := strconv.ParseInt(raw, 10, 32)
				if err != nil {
					panic(ParsingError{
						"invalid integer literal '" + raw + "'",
						i,
						start,
						KnownType,
						(*IntLiteral)(nil),
					})
				}

				literal = &IntLiteral{
					NodeBase: NodeBase{
						NodeSpan{start, i},
					},
					Raw:   raw,
					Value: int(integer),
				}

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
						unit := parseExpression()
						ident, ok := unit.(*IdentifierLiteral)
						raw := string(s[start:i])

						if !ok {
							panic(ParsingError{
								"invalid rate literal '" + raw + "', '/' should be immeditately followed by an identifier ('s' for example)",
								i,
								start,
								KnownType,
								(*IntLiteral)(nil),
							})
						}

						return &RateLiteral{
							NodeBase: NodeBase{
								NodeSpan{literal.Base().Span.Start, ident.Base().Span.End},
							},
							Quantity: literal.(*QuantityLiteral),
							Unit:     ident,
						}
					}
				}
			}

			return literal

		case '{': //object
			openingBraceIndex := i
			i++

			unamedPropCount := 0
			var properties []ObjectProperty

			for i < len(s) && s[i] != '}' {
				eatSpaceNewlineComma()

				if i < len(s) && s[i] == '}' {
					break
				}

				var keys []Node
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
						panic(ParsingError{
							"key is too long",
							i,
							openingBraceIndex,
							KnownType,
							(*ObjectLiteral)(nil),
						})
					}
				} else {
					for {
						lastKey = parseExpression()
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
					panic(ParsingError{
						"invalid object literal, missing value after colon, key '" + lastKeyName + "'",
						i,
						openingBraceIndex,
						KnownType,
						(*ObjectLiteral)(nil),
					})
				}
				v := parseExpression()

				if len(keys) > 1 {
					switch v.(type) {
					case *Variable, *GlobalVariable:
					default:
						if !isSimpleValueLiteral(v) {
							panic(ParsingError{
								"invalid object literal, the value of a multi-key property definition should be a simple literal or a variable, last key is '" + lastKeyName + "'",
								i,
								openingBraceIndex,
								KnownType,
								(*ObjectLiteral)(nil),
							})
						}
					}

				}

				for _, key := range keys {
					properties = append(properties, ObjectProperty{
						NodeBase: NodeBase{
							Span: NodeSpan{propSpanStart, i},
						},
						Key:   key,
						Value: v,
					})
				}

				eatSpaceNewlineComma()
			}

			if i >= len(s) {
				panic(ParsingError{
					"unterminated object literal, missing closing brace '}'",
					i,
					openingBraceIndex,
					KnownType,
					(*ObjectLiteral)(nil),
				})
			}
			i++

			return &ObjectLiteral{
				NodeBase: NodeBase{
					Span: NodeSpan{openingBraceIndex, i},
				},
				Properties: properties,
			}
		case '[': //list
			openingBracketIndex := i
			i++

			var elements []Node
			for i < len(s) && s[i] != ']' {
				eatSpaceNewlineComma()

				if i < len(s) && s[i] == ']' {
					break
				}

				e := parseExpression()
				elements = append(elements, e)

				eatSpaceNewlineComma()
			}

			if i >= len(s) || s[i] != ']' {
				panic(ParsingError{
					"unterminated list literal, missing closing bracket ']'",
					i,
					openingBracketIndex,
					KnownType,
					(*ListLiteral)(nil),
				})
			}
			i++

			return &ListLiteral{
				NodeBase: NodeBase{
					Span: NodeSpan{openingBracketIndex, i},
				},
				Elements: elements,
			}
		case '"': //string
			//strings are JSON strings
			start := i
			i++

			for i < len(s) && (s[i] != '"' || countPrevBackslashes()%2 == 1) {
				i++
			}

			if i >= len(s) && s[i-1] != '"' {
				panic(ParsingError{
					"unterminated string literal '" + string(s[start:]) + "'",
					i,
					start,
					KnownType,
					(*StringLiteral)(nil),
				})
			}
			i++
			raw := string(s[start:i])

			var value string

			err := json.Unmarshal([]byte(raw), &value)

			if err != nil {
				panic(ParsingError{
					"invalid string literal '" + raw + "': " + err.Error(),
					i,
					start,
					KnownType,
					(*StringLiteral)(nil),
				})
			}

			return &StringLiteral{
				NodeBase: NodeBase{
					Span: NodeSpan{start, i},
				},
				Raw:   raw,
				Value: value,
			}
		case '/':
			return parsePathLikeExpression()
		case '.':
			if i < len(s)-1 {
				if s[i+1] == '/' || i < len(s)-2 && s[i+1] == '.' && s[i+2] == '/' {
					return parsePathLikeExpression()
				}
				switch s[i+1] {
				case '{':
					return parseKeyList()
				case '.':
					start := i
					i += 2

					upperBound := parseExpression()
					expr := &UpperBoundRangeExpression{
						NodeBase: NodeBase{
							NodeSpan{start, i},
						},
						UpperBound: upperBound,
					}

					return expr
				}
			}
			//otherwise fail
		case '@': //lazy
			start := i
			i++
			if i >= len(s) {
				panic(ParsingError{
					"invalid lazy expression, '@' should be followed by an expression",
					i,
					start,
					KnownType,
					(*LazyExpression)(nil),
				})
			}

			e := parseExpression()

			return &LazyExpression{
				NodeBase: NodeBase{
					Span: NodeSpan{start, i},
				},
				Expression: e,
			}
		case '(': //parenthesized expression and binary expressions
			openingParenIndex := i
			i++
			left := parseExpression()

			eatSpace()

			if i < len(s) && s[i] == ')' {
				i++
				lhs = left
				parenthesizedFirstStart = openingParenIndex
				break
			}

			UNTERMINATED_BIN_EXPR := "unterminated binary expression:"
			NON_EXISTING_OPERATOR := "invalid binary expression, non existing operator"

			if i >= len(s) {
				panic(ParsingError{
					UNTERMINATED_BIN_EXPR + " missing operator",
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			}

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
					panic(ParsingError{
						NON_EXISTING_OPERATOR,
						i,
						openingParenIndex,
						KnownType,
						(*BinaryExpression)(nil),
					})
				}
				if s[i] == '=' {
					operator = NotEqual
					break
				}
				panic(ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			case '=':
				i++
				if i >= len(s) {
					panic(ParsingError{
						NON_EXISTING_OPERATOR,
						i,
						openingParenIndex,
						KnownType,
						(*BinaryExpression)(nil),
					})
				}
				if s[i] == '=' {
					operator = Equal
					break
				}
				panic(ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			case 'a':
				AND_LEN := len("and")
				if len(s)-i >= AND_LEN && string(s[i:i+AND_LEN]) == "and" {
					operator = And
					i += AND_LEN - 1
					break
				}
				panic(ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			case 'i':
				i++
				if i >= len(s) {
					panic(ParsingError{
						UNTERMINATED_BIN_EXPR,
						i,
						openingParenIndex,
						KnownType,
						(*BinaryExpression)(nil),
					})
				}
				if s[i] == 'n' {
					operator = In
					break
				}
				panic(ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			case 'k':
				KEYOF_LEN := len("keyof")
				if len(s)-i >= KEYOF_LEN && string(s[i:i+KEYOF_LEN]) == "keyof" {
					operator = Keyof
					i += KEYOF_LEN - 1
					break
				}
				panic(ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			case 'n':
				NOTIN_LEN := len("not-in")
				if len(s)-i >= NOTIN_LEN && string(s[i:i+NOTIN_LEN]) == "not-in" {
					operator = NotIn
					i += NOTIN_LEN - 1
					break
				}
				panic(ParsingError{
					NON_EXISTING_OPERATOR,
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			case 'o':
				OR_LEN := len("or")
				if len(s)-i >= OR_LEN && string(s[i:i+OR_LEN]) == "or" {
					operator = Or
					i += OR_LEN - 1
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
					panic(ParsingError{
						"invalid binary expression, non existing operator",
						i,
						openingParenIndex,
						KnownType,
						(*BinaryExpression)(nil),
					})
				}
			}

			if operator == Range && i < len(s) && s[i] == '<' {
				operator = ExclEndRange
				i++
			}

			eatSpace()

			if i >= len(s) {
				panic(ParsingError{
					UNTERMINATED_BIN_EXPR + " missing right operand",
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			}

			right := parseExpression()

			eatSpace()
			if i >= len(s) {
				panic(ParsingError{
					UNTERMINATED_BIN_EXPR + " missing closing parenthesis",
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			}

			if s[i] != ')' {
				panic(ParsingError{
					"invalid binary expression",
					i,
					openingParenIndex,
					KnownType,
					(*BinaryExpression)(nil),
				})
			}

			i++

			lhs = &BinaryExpression{
				NodeBase: NodeBase{
					Span: NodeSpan{openingParenIndex, i},
				},
				Operator: operator,
				Left:     left,
				Right:    right,
			}
		}

		first = lhs

		//member expressions, index/slice expressions
		if lhs != nil && i < len(s) && (s[i] == '[' || s[i] == '.') {
			i++

			for {
				start := i

				if i >= len(s) {
					panic(ParsingError{
						"unterminated member/index expression",
						i,
						first.Base().Span.Start,
						UnspecifiedCategory,
						nil,
					})
				}

				if s[i-1] == '[' {
					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated index expression",
							i,
							first.Base().Span.Start,
							KnownType,
							nil,
						})
					}

					var startIndex Node
					var endIndex Node
					isSliceExpr := s[i] == ':'

					if isSliceExpr {
						i++
					} else {
						startIndex = parseExpression()
					}

					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated index/slice expression",
							i,
							first.Base().Span.Start,
							UnspecifiedCategory,
							nil,
						})
					}

					if s[i] == ':' {
						if isSliceExpr {
							panic(ParsingError{
								"invalid slice expression, a single colon should be present",
								i,
								first.Base().Span.Start,
								KnownType,
								(*SliceExpression)(nil),
							})
						}
						isSliceExpr = true
						i++
					}

					eatSpace()

					if isSliceExpr && startIndex == nil && (i >= len(s) || s[i] == ']') {
						panic(ParsingError{
							"unterminated slice expression, missing end index",
							i,
							first.Base().Span.Start,
							KnownType,
							(*SliceExpression)(nil),
						})
					}

					if i < len(s) && s[i] != ']' && isSliceExpr {
						endIndex = parseExpression()
					}

					eatSpace()

					if i >= len(s) || s[i] != ']' {
						panic(ParsingError{
							"unterminated index/slice expression, missing closing bracket ']'",
							i,
							first.Base().Span.Start,
							UnspecifiedCategory,
							nil,
						})
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
							},
							Indexed:    lhs,
							StartIndex: startIndex,
							EndIndex:   endIndex,
						}
					}

					lhs = &IndexExpression{
						NodeBase: NodeBase{
							NodeSpan{spanStart, i},
						},
						Indexed: lhs,
						Index:   startIndex,
					}
				} else {
					if !isAlpha(s[i]) && s[i] != '_' {
						panic(ParsingError{
							"property name should start with a letter not '" + string(s[i]) + "'",
							i,
							first.Base().Span.Start,
							KnownType,
							(*MemberExpression)(nil),
						})
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
						},
						Left: lhs,
						PropertyName: &IdentifierLiteral{
							NodeBase: NodeBase{
								NodeSpan{start, i},
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

		//call
		if lhs != nil && i < len(s) && s[i] == '(' {
			i++
			spanStart := lhs.Base().Span.Start

			if lhs == first {
				spanStart = parenthesizedFirstStart
			}

			call := &Call{
				NodeBase: NodeBase{
					NodeSpan{spanStart, 0},
				},
				Callee:    lhs,
				Arguments: nil,
			}

			for i < len(s) && s[i] != ')' {
				eatSpaceNewlineComma()

				if i >= len(s) || s[i] == ')' {
					break
				}

				arg := parseExpression()

				call.Arguments = append(call.Arguments, arg)
				eatSpaceNewlineComma()
			}

			if i >= len(s) || s[i] != ')' {
				panic(ParsingError{
					"unterminated call, missing closing parenthesis ')'",
					i,
					first.Base().Span.Start,
					KnownType,
					(*Call)(nil),
				})
			}
			i++

			if i < len(s) && s[i] == '!' {
				call.Must = true
				i++
			}

			call.NodeBase.Span.End = i
			return call
		}

		if lhs != nil {
			return lhs
		}

		left := string(s[max(0, i-5):i])
		right := string(s[i:min(len(s), i+5)])

		panic(ParsingError{
			fmt.Sprintf("an expression was expected: ...%s<<here>>%s...", left, right),
			i,
			first.Base().Span.Start,
			UnspecifiedCategory,
			nil,
		})
	}

	parseRequirements = func() *Requirements {
		var requirements *Requirements
		if i < len(s) && strings.HasPrefix(string(s[i:]), "require") {
			i += len("require")

			eatSpace()
			requirementObject := parseExpression()
			requirements = &Requirements{
				Object: requirementObject.(*ObjectLiteral),
			}

		}
		return requirements
	}

	parseGlobalConstantDeclarations = func() *GlobalConstantDeclarations {
		start := i
		if i < len(s) && strings.HasPrefix(string(s[i:]), "const") {
			i += len("const")

			eatSpace()

			if i >= len(s) {
				panic(ParsingError{
					"unterminated global const declarations",
					i,
					start,
					KnownType,
					(*GlobalConstantDeclarations)(nil),
				})
			}

			if s[i] != '(' {
				panic(ParsingError{
					"invalid global const declarations, expected opening parenthesis after 'const'",
					i,
					start,
					KnownType,
					(*GlobalConstantDeclarations)(nil),
				})
			}

			i++
			var namesValues [][2]Node

			for i < len(s) && s[i] != ')' {
				eatSpaceAndNewLineAndComment()

				if i < len(s) && s[i] == ')' {
					break
				}

				if i >= len(s) {
					panic(ParsingError{
						"invalid global const declarations, missing closing parenthesis",
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					})
				}

				lhs := parseExpression()
				globvar, ok := lhs.(*IdentifierLiteral)
				if !ok {
					panic(ParsingError{
						"invalid global const declarations, left hand sides must be identifiers",
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					})
				}

				eatSpace()

				if i >= len(s) || s[i] != '=' {
					panic(ParsingError{
						fmt.Sprintf("invalid global const declarations, missing '=' after name %s", globvar.Name),
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					})
				}

				i++
				eatSpace()

				if i >= len(s) || s[i] == ')' {
					panic(ParsingError{
						fmt.Sprintf("invalid global const declarations, missing value after '$$%s ='", globvar.Name),
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					})
				}

				rhs := parseExpression()
				if !isSimpleValueLiteral(rhs) {
					panic(ParsingError{
						fmt.Sprintf("invalid global const declarations, only literals are allowed as values : %T", rhs),
						i,
						start,
						KnownType,
						(*GlobalConstantDeclarations)(nil),
					})
				}

				namesValues = append(namesValues, [2]Node{lhs, rhs})

				eatSpaceAndNewLineAndComment()
			}

			i++

			decls := &GlobalConstantDeclarations{
				NodeBase: NodeBase{
					NodeSpan{start, i},
				},
				NamesValues: namesValues,
			}

			return decls
		}

		return nil
	}

	parseFunction = func(start int) Node {
		eatSpace()

		var ident *IdentifierLiteral

		if i < len(s) && isAlpha(s[i]) {
			idnt := parseIdentLike()
			var ok bool
			if ident, ok = idnt.(*IdentifierLiteral); !ok {
				panic(ParsingError{
					fmt.Sprintf("function name should be an identifier not a %T", idnt),
					i,
					start,
					KnownType,
					(*FunctionDeclaration)(nil),
				})
			}
		}

		if i >= len(s) || s[i] != '(' {
			panic(ParsingError{
				"function : fn keyword (or function name) should be followed by '(' <param list> ')' ",
				i,
				start,
				UnspecifiedCategory,
				nil,
			})
		}

		i++

		var parameters []FunctionParameter

		for i < len(s) && s[i] != ')' {
			eatSpaceNewlineComma()

			if i < len(s) && s[i] == ')' {
				break
			}

			varNode := parseExpression()

			if _, ok := varNode.(*IdentifierLiteral); !ok {
				panic(ParsingError{
					"function : the parameter list should contain variables separated by a comma",
					i,
					start,
					UnspecifiedCategory,
					nil,
				})
			}

			parameters = append(parameters, FunctionParameter{
				Var: varNode.(*IdentifierLiteral),
			})

			eatSpaceNewlineComma()
		}

		if i >= len(s) {
			panic(ParsingError{
				"function : unterminated parameter list : missing closing parenthesis",
				i,
				start,
				UnspecifiedCategory,
				nil,
			})
		}

		if s[i] != ')' {
			panic(ParsingError{
				"function : invalid syntax",
				i,
				start,
				UnspecifiedCategory,
				nil,
			})
		}

		i++

		eatSpace()

		requirements := parseRequirements()

		eatSpace()
		if i >= len(s) || s[i] != '{' {
			panic(ParsingError{
				"function : parameter list should be followed by a block, not " + string(s[i]),
				i,
				start,
				UnspecifiedCategory,
				nil,
			})
		}

		blk := parseBlock()
		fn := FunctionExpression{
			NodeBase: NodeBase{
				Span: NodeSpan{start, blk.Span.End},
			},
			Parameters:   parameters,
			Body:         blk,
			Requirements: requirements,
		}

		if ident != nil {
			return &FunctionDeclaration{
				NodeBase: NodeBase{
					Span: fn.Span,
				},
				Function: &fn,
				Name:     ident,
			}
		}

		return &fn
	}

	parseStatement = func() Statement {
		expr := parseExpression()

		if i >= len(s) {
			return expr
		}

		b := s[i]
		followedBySpace := b == ' '

		switch ev := expr.(type) {
		case *Call:
			return ev
		case *IdentifierLiteral:
			switch ev.Name {
			case "if":
				eatSpace()
				test := parseExpression()
				eatSpace()

				if i >= len(s) {
					panic(ParsingError{
						"unterminated if statement, missing block",
						i,
						expr.Base().Span.Start,
						KnownType,
						(*IfStatement)(nil),
					})
				}

				if s[i] != '{' {
					panic(ParsingError{
						"invalid if statement, test expression should be followed by a block, not " + string(s[i]),
						i,
						expr.Base().Span.Start,
						KnownType,
						(*IfStatement)(nil),
					})
				}

				blk := parseBlock()
				end := blk.Span.End
				eatSpace()

				var alternate *Block

				if i < len(s)-4 && string(s[i:i+4]) == "else" {
					i += 4
					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated if statement, missing block after 'else'",
							i,
							expr.Base().Span.Start,
							KnownType,
							(*IfStatement)(nil),
						})
					}

					if s[i] != '{' {
						panic(ParsingError{
							"invalid if statement, else should be followed by a block, not " + string(s[i]),
							i,
							expr.Base().Span.Start,
							KnownType,
							(*IfStatement)(nil),
						})
					}

					alternate = parseBlock()
					end = alternate.Span.End
				}

				return &IfStatement{
					NodeBase: NodeBase{
						Span: NodeSpan{ev.Span.Start, end},
					},
					Test:       test,
					Consequent: blk,
					Alternate:  alternate,
				}
			case "for":
				forStart := expr.Base().Span.Start
				eatSpace()
				keyIndexIdent := parseExpression()

				switch v := keyIndexIdent.(type) {
				case *IdentifierLiteral:
					eatSpace()

					if i > len(s) {
						panic(ParsingError{
							"invalid for statement",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					if s[i] != ',' {
						panic(ParsingError{
							"for statement : key/index name should be followed by a comma ',' , not " + string(s[i]),
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					i++
					eatSpace()

					if i > len(s) {
						panic(ParsingError{
							"unterminated for statement",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					valueElemIdent := parseExpression()

					if _, isVar := valueElemIdent.(*IdentifierLiteral); !isVar {
						panic(ParsingError{
							fmt.Sprintf("invalid for statement : 'for <key-index var> <colon> should be followed by a variable, not a(n) %T", keyIndexIdent),
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated for statement",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					if s[i] != 'i' || i > len(s)-2 || s[i+1] != 'n' {
						panic(ParsingError{
							"invalid for statement : missing 'in' keyword ",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					i += 2

					if i < len(s) && s[i] != ' ' {
						panic(ParsingError{
							"invalid for statement : 'in' keyword should be followed by a space",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}
					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated for statement, missing value after 'in'",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					iteratedValue := parseExpression()

					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated for statement, missing block",
							i,
							forStart,
							KnownType,
							(*ForStatement)(nil),
						})
					}

					blk := parseBlock()

					return &ForStatement{
						NodeBase: NodeBase{
							Span: NodeSpan{ev.Span.Start, blk.Span.End},
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

						if i >= len(s) {
							panic(ParsingError{
								"unterminated for statement, missing block",
								i,
								forStart,
								KnownType,
								(*ForStatement)(nil),
							})
						}

						blk := parseBlock()

						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, blk.Span.End},
							},
							KeyIndexIdent:  nil,
							ValueElemIdent: nil,
							Body:           blk,
							IteratedValue:  iteratedValue,
						}
					}
					panic(ParsingError{
						fmt.Sprintf("invalid for statement : 'for' should be followed by a binary range expression, operator is %s", v.Operator.String()),
						i,
						forStart,
						KnownType,
						(*ForStatement)(nil),
					})
				default:
					panic(ParsingError{
						fmt.Sprintf("invalid for statement : 'for' should be followed by a variable or a binary range expression (binary range operator), not a(n) %T", keyIndexIdent),
						i,
						forStart,
						KnownType,
						(*ForStatement)(nil),
					})
				}

			case "switch", "match":
				switchMatchStart := expr.Base().Span.Start

				eatSpace()

				if i >= len(s) {

					if ev.Name == "switch" {
						panic(ParsingError{
							"unterminated switch statement: missing value",
							i,
							switchMatchStart,
							KnownType,
							(*SwitchStatement)(nil),
						})
					}

					panic(ParsingError{
						"unterminated match statement: missing value",
						i,
						switchMatchStart,
						KnownType,
						(*MatchStatement)(nil),
					})
				}

				discriminant := parseExpression()
				var switchCases []*Case

				eatSpace()

				if i >= len(s) || s[i] != '{' {
					if ev.Name == "switch" {
						panic(ParsingError{
							"unterminated switch statement : missing body",
							i,
							switchMatchStart,
							KnownType,
							(*SwitchStatement)(nil),
						})
					}
					panic(ParsingError{
						"unterminated match statement : missing body",
						i,
						switchMatchStart,
						KnownType,
						(*MatchStatement)(nil),
					})
				}

				i++

				for i < len(s) && s[i] != '}' {
					eatSpaceNewLineSemiColonComment()

					var valueNodes []Node

					for i < len(s) && s[i] != '{' {
						if i >= len(s) {
							if ev.Name == "switch" {
								panic(ParsingError{
									"unterminated switch statement",
									i,
									switchMatchStart,
									KnownType,
									(*SwitchStatement)(nil),
								})
							}

							panic(ParsingError{
								"unterminated match statement",
								i,
								switchMatchStart,
								KnownType,
								(*MatchStatement)(nil),
							})
						}
						valueNode := parseExpression()
						if !isSimpleValueLiteral(valueNode) {
							if ev.Name == "switch" {
								panic(ParsingError{
									"invalid switch case : only simple value literals are supported (1, 1.0, /home, ..)",
									i,
									switchMatchStart,
									KnownType,
									(*SwitchStatement)(nil),
								})
							}
							panic(ParsingError{
								"invalid match case : only simple value literals are supported (1, 1.0, /home, ..)",
								i,
								switchMatchStart,
								KnownType,
								(*MatchStatement)(nil),
							})

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
							panic(ParsingError{
								"invalid switch case : missing block",
								i,
								switchMatchStart,
								KnownType,
								(*SwitchStatement)(nil),
							})
						}
						panic(ParsingError{
							"invalid match case : missing block",
							i,
							switchMatchStart,
							KnownType,
							(*MatchStatement)(nil),
						})
					}

					blk := parseBlock()

					for _, valNode := range valueNodes {
						switchCase := &Case{
							NodeBase: NodeBase{
								NodeSpan{valNode.Base().Span.Start, blk.Span.End},
							},
							Value: valNode,
							Block: blk,
						}

						switchCases = append(switchCases, switchCase)
					}

					eatSpaceNewLineSemiColonComment()
				}

				if i >= len(s) || s[i] != '}' {
					if ev.Name == "switch" {
						panic(ParsingError{
							"unterminated switch statement : missing closing body brace '}'",
							i,
							switchMatchStart,
							KnownType,
							(*SwitchStatement)(nil),
						})
					}
					panic(ParsingError{
						"unterminated match statement : missing closing body brace '}'",
						i,
						switchMatchStart,
						KnownType,
						(*MatchStatement)(nil),
					})

				}

				i++

				if ev.Name == "switch" {

					return &SwitchStatement{
						NodeBase: NodeBase{
							NodeSpan{ev.Span.Start, i},
						},
						Discriminant: discriminant,
						Cases:        switchCases,
					}
				}

				return &MatchStatement{
					NodeBase: NodeBase{
						NodeSpan{ev.Span.Start, i},
					},
					Discriminant: discriminant,
					Cases:        switchCases,
				}

			case "fn":
				fn := parseFunction(ev.Span.Start)

				return fn
			case "import":
				importStart := expr.Base().Span.Start

				eatSpace()

				identifier := parseIdentLike()
				if _, ok := identifier.(*IdentifierLiteral); !ok {
					panic(ParsingError{
						"import statement: import should be followed by an identifier",
						i,
						importStart,
						KnownType,
						(*ImportStatement)(nil),
					})

				}

				eatSpace()

				url_ := parseExpression()
				if _, ok := url_.(*URLLiteral); !ok {
					panic(ParsingError{
						"import statement: URL should be a URL literal",
						i,
						importStart,
						KnownType,
						(*ImportStatement)(nil),
					})
				}

				eatSpace()

				checksum := parseExpression()
				if _, ok := checksum.(*StringLiteral); !ok {
					panic(ParsingError{
						"import statement: checksum should be a string literal",
						i,
						importStart,
						KnownType,
						(*ImportStatement)(nil),
					})
				}

				eatSpace()

				argumentObject := parseExpression()
				if _, ok := argumentObject.(*ObjectLiteral); !ok {
					panic(ParsingError{
						"import statement: argument should be an object literal",
						i,
						importStart,
						KnownType,
						(*ImportStatement)(nil),
					})
				}

				eatSpace()
				allowIdent := parseExpression()
				if ident, ok := allowIdent.(*IdentifierLiteral); !ok || ident.Name != "allow" {
					panic(ParsingError{
						"import statement: argument should be followed by a the 'allow' keyword",
						i,
						importStart,
						KnownType,
						(*ImportStatement)(nil),
					})
				}

				eatSpace()
				grantedPerms := parseExpression()
				grantedPermsLit, ok := grantedPerms.(*ObjectLiteral)
				if !ok {
					panic(ParsingError{
						"import statement: 'allow' keyword should be followed by an object literal (permissions)",
						i,
						importStart,
						KnownType,
						(*ImportStatement)(nil),
					})
				}

				return &ImportStatement{
					NodeBase: NodeBase{
						NodeSpan{ev.Span.Start, i},
					},
					Identifier:         identifier.(*IdentifierLiteral),
					URL:                url_.(*URLLiteral),
					ValidationString:   checksum.(*StringLiteral),
					ArgumentObject:     argumentObject.(*ObjectLiteral),
					GrantedPermissions: grantedPermsLit,
				}

			case "return":
				eatSpace()

				returnValue := parseExpression()
				return &ReturnStatement{
					NodeBase: NodeBase{
						Span: NodeSpan{ev.Span.Start, returnValue.Base().Span.End},
					},
					Expr: returnValue,
				}
			case "break":
				return &BreakStatement{
					NodeBase: NodeBase{
						Span: ev.Span,
					},
					Label: nil,
				}
			case "continue":
				return &ContinueStatement{
					NodeBase: NodeBase{
						Span: ev.Span,
					},
					Label: nil,
				}
			case "assign":
				var vars []Node

				for i < len(s) && s[i] != '=' {
					eatSpace()
					e := parseExpression()
					if _, ok := e.(*IdentifierLiteral); !ok {
						panic(ParsingError{
							"assign keyword should be followed by identifiers (assign a b = <value>)",
							i,
							expr.Base().Span.Start,
							KnownType,
							(*MultiAssignment)(nil),
						})
					}
					vars = append(vars, e)
					eatSpace()

				}

				if i >= len(s) {
					panic(ParsingError{
						"unterminated multi assign statement, missing '='",
						i,
						expr.Base().Span.Start,
						KnownType,
						(*MultiAssignment)(nil),
					})
				}

				i++
				eatSpace()
				right := parseExpression()

				return &MultiAssignment{
					NodeBase: NodeBase{
						Span: NodeSpan{ev.Span.Start, right.Base().Span.End},
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
				panic(ParsingError{
					"unterminated assignment, missing value after '='",
					i,
					expr.Base().Span.Start,
					KnownType,
					(*Assignment)(nil),
				})
			}
			right := parseExpression()

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
			case *IdentifierLiteral, *IdentifierMemberExpression:
				if !followedBySpace || s[i] == '\n' || (isNotPairedOrIsClosingDelim(s[i]) && s[i] != '(') {
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

				for i < len(s) && s[i] != '\n' && !isNotPairedOrIsClosingDelim(s[i]) {
					eatSpaceAndComments()

					if s[i] == '\n' || isNotPairedOrIsClosingDelim(s[i]) {
						break
					}

					arg := parseExpression()

					call.Arguments = append(call.Arguments, arg)
					eatSpaceAndComments()
				}

				if i < len(s) && s[i] == '\n' {
					i++
				}

				if len(call.Arguments) == 0 {
					call.NodeBase.Span.End = expr.Base().Span.End
				} else {
					call.NodeBase.Span.End = call.Arguments[len(call.Arguments)-1].Base().Span.End
				}

				return call
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
		stmts = append(stmts, parseStatement())
		eatSpaceNewLineSemiColonComment()
	}

	mod.Requirements = requirements
	mod.Statements = stmts
	mod.GlobalConstantDeclarations = globalConstDecls

	return mod, nil
}

func IsSimpleGopherVal(v interface{}) bool {
	switch v.(type) {
	case string, JSONstring, bool, int, float64,
		Identifier, Path, PathPattern, URL, HTTPHost, HTTPHostPattern, URLPattern:
		return true
	default:
		return false
	}
}

func IsGopherVal(v interface{}) bool {
	switch v.(type) {
	case string, JSONstring, bool, int, float64, Object, List, Func, ExternalValue,
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
	Name string
	Rate ByteRate
}

type Limiter struct {
	limitation Limitation
	bucket     *TokenBucket
}

type Context struct {
	grantedPermissions   []Permission
	forbiddenPermissions []Permission
	limitations          []Limitation
	limiters             map[string]*Limiter
	stackPermission      StackPermission
}

type ContextConfig struct {
	grantedPermissions   []Permission
	forbiddenPermissions []Permission
	limitations          []Limitation
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

	for _, l := range limitations {
		limiters[l.Name] = &Limiter{
			limitation: l,
			//Buckets all have the same tick interval. Calculating the interval from the rate
			//can result in small values (< 5ms) that are too precise and cause issues.
			bucket: newBucket(TOKEN_BUCKET_INTERVAL, int64(l.Rate), int64(l.Rate)/100),
		}
	}

	ctx := &Context{
		grantedPermissions:   permissions,
		forbiddenPermissions: forbiddenPermissions,
		limitations:          limitations,
		limiters:             limiters,
		stackPermission:      stackPermission,
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

//Creates a new Context  with the permissions passed as argument removed.
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

func (ctx *Context) Take(name string, count int64) {
	limiter, ok := ctx.limiters[name]
	if ok {
		limiter.bucket.Take(count)
	}
}

func (ctx *Context) GetRate(name string) ByteRate {
	limiter, ok := ctx.limiters[name]
	if ok {
		return limiter.limitation.Rate
	}
	return -1
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
	ctx       *Context
	constants map[string]int
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

func memb(value interface{}, name string) (interface{}, *reflect.Type, error) {
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
		return nil, nil, errors.New("cannot get property of non object/Go value")
	}
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
			{"http/upload", ByteRate(100_000)},
			{"http/download", ByteRate(100_000)},
			{"fs/read", ByteRate(1_000_000)},
			{"fs/write", ByteRate(100_000)},
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

func Walk(node, parent Node, ancestorChain *[]Node, fn func(Node, Node, []Node) error) error {
	//refactor with panics ?

	if ancestorChain != nil {
		*ancestorChain = append((*ancestorChain), parent)
		defer func() {
			*ancestorChain = (*ancestorChain)[:len(*ancestorChain)-1]
		}()
	}

	if err := fn(node, parent, *ancestorChain); err != nil {
		return err
	}

	switch n := node.(type) {
	case *Module:
		if n.Requirements != nil {
			if err := Walk(n.Requirements.Object, node, ancestorChain, fn); err != nil {
				return err
			}
		}

		if n.GlobalConstantDeclarations != nil {
			if err := Walk(n.GlobalConstantDeclarations, node, ancestorChain, fn); err != nil {
				return err
			}
		}

		for _, stmt := range n.Statements {
			if err := Walk(stmt, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *EmbeddedModule:
		if n.Requirements != nil {
			if err := Walk(n.Requirements.Object, node, ancestorChain, fn); err != nil {
				return err
			}
		}

		for _, stmt := range n.Statements {
			if err := Walk(stmt, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *ImportStatement:
		if err := Walk(n.Identifier, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.URL, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.ValidationString, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.ArgumentObject, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.GrantedPermissions, node, ancestorChain, fn); err != nil {
			return err
		}
	case *SpawnExpression:
		if n.GroupIdent != nil {
			if err := Walk(n.GroupIdent, node, ancestorChain, fn); err != nil {
				return err
			}
		}
		if err := Walk(n.Globals, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.ExprOrVar, node, ancestorChain, fn); err != nil {
			return err
		}
		if n.GrantedPermissions != nil {
			if err := Walk(n.GrantedPermissions, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *Block:
		for _, stmt := range n.Statements {
			if err := Walk(stmt, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *FunctionDeclaration:
		if err := Walk(n.Name, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Function, node, ancestorChain, fn); err != nil {
			return err
		}
	case *FunctionExpression:
		for _, p := range n.Parameters {
			if err := Walk(p.Var, node, ancestorChain, fn); err != nil {
				return err
			}
		}
		if err := Walk(n.Body, node, ancestorChain, fn); err != nil {
			return err
		}

		if n.Requirements != nil {
			if err := Walk(n.Requirements.Object, node, ancestorChain, fn); err != nil {
				return err
			}
		}

	case *ObjectLiteral:
		for _, prop := range n.Properties {
			if err := Walk(&prop, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *ObjectProperty:
		if n.Key != nil {
			if err := Walk(n.Key, node, ancestorChain, fn); err != nil {
				return err
			}
		}

		if err := Walk(n.Value, node, ancestorChain, fn); err != nil {
			return err
		}
	case *MemberExpression:
		if err := Walk(n.Left, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.PropertyName, node, ancestorChain, fn); err != nil {
			return err
		}
	case *IndexExpression:
		if err := Walk(n.Indexed, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Index, node, ancestorChain, fn); err != nil {
			return err
		}
	case *SliceExpression:
		if err := Walk(n.Indexed, node, ancestorChain, fn); err != nil {
			return err
		}
		if n.StartIndex != nil {
			if err := Walk(n.StartIndex, node, ancestorChain, fn); err != nil {
				return err
			}
		}
		if n.EndIndex != nil {
			if err := Walk(n.EndIndex, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *IdentifierMemberExpression:
		if err := Walk(n.Left, node, ancestorChain, fn); err != nil {
			return err
		}
		for _, p := range n.PropertyNames {
			if err := Walk(p, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *KeyListExpression:
		for _, key := range n.Keys {
			if err := Walk(key, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *Assignment:
		if err := Walk(n.Left, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Right, node, ancestorChain, fn); err != nil {
			return err
		}
	case *MultiAssignment:
		for _, vr := range n.Variables {
			if err := Walk(vr, node, ancestorChain, fn); err != nil {
				return err
			}
		}
		if err := Walk(n.Right, node, ancestorChain, fn); err != nil {
			return err
		}
	case *Call:
		Walk(n.Callee, node, ancestorChain, fn)
		for _, arg := range n.Arguments {
			if err := Walk(arg, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *IfStatement:
		if err := Walk(n.Test, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Consequent, node, ancestorChain, fn); err != nil {
			return err
		}
		if n.Alternate != nil {
			if err := Walk(n.Alternate, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *ForStatement:
		if n.KeyIndexIdent != nil {
			if err := Walk(n.KeyIndexIdent, node, ancestorChain, fn); err != nil {
				return err
			}
			if err := Walk(n.ValueElemIdent, node, ancestorChain, fn); err != nil {
				return err
			}
		}

		if err := Walk(n.IteratedValue, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Body, node, ancestorChain, fn); err != nil {
			return err
		}
	case *ReturnStatement:
		if err := Walk(n.Expr, node, ancestorChain, fn); err != nil {
			return err
		}
	case *BreakStatement:
		if n.Label != nil {
			if err := Walk(n.Label, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *ContinueStatement:
		if n.Label != nil {
			if err := Walk(n.Label, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *SwitchStatement:
		if err := Walk(n.Discriminant, node, ancestorChain, fn); err != nil {
			return err
		}
		for _, switcCase := range n.Cases {
			if err := Walk(switcCase, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *MatchStatement:
		if err := Walk(n.Discriminant, node, ancestorChain, fn); err != nil {
			return err
		}
		for _, switcCase := range n.Cases {
			if err := Walk(switcCase, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *Case:
		if err := Walk(n.Value, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Block, node, ancestorChain, fn); err != nil {
			return err
		}
	case *LazyExpression:
		if err := Walk(n.Expression, node, ancestorChain, fn); err != nil {
			return err
		}
	case *BinaryExpression:
		if err := Walk(n.Left, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Right, node, ancestorChain, fn); err != nil {
			return err
		}
	case *UpperBoundRangeExpression:
		if err := Walk(n.UpperBound, node, ancestorChain, fn); err != nil {
			return err
		}
	case *AbsolutePathExpression:
		for _, e := range n.Slices {
			if err := Walk(e, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *RelativePathExpression:
		for _, e := range n.Slices {
			if err := Walk(e, node, ancestorChain, fn); err != nil {
				return err
			}
		}
	case *URLExpression:
		if err := Walk(n.Path, node, ancestorChain, fn); err != nil {
			return err
		}
	case *RateLiteral:
		if err := Walk(n.Quantity, node, ancestorChain, fn); err != nil {
			return err
		}
		if err := Walk(n.Unit, node, ancestorChain, fn); err != nil {
			return err
		}
	}

	return nil
}

type globalVarInfo struct {
	isConst bool
}

func Check(node Node) error {

	//key: *Module|*EmbeddedModule
	fnDecls := make(map[Node]map[string]int)

	//key: *Module|*EmbeddedModule|*Block
	globalVars := make(map[Node]map[string]globalVarInfo)

	//key: *Module|*EmbeddedModule|*Block
	localVars := make(map[Node]map[string]int)

	parentChain := make([]Node, 0)

	return Walk(node, nil, &parentChain, func(n Node, parent Node, ancestorChain []Node) error {

		switch node := n.(type) {
		case *QuantityLiteral:
			switch node.Unit {
			case "s", "ms", "%", "ln", "kB", "MB", "GB":
			default:
				return errors.New("non supported unit: " + node.Unit)
			}
		case *RateLiteral:

			unit1 := node.Quantity.Unit
			unit2 := node.Unit.Name

			switch unit2 {
			case "s":
				switch unit1 {
				case "kB", "MB", "GB":
					return nil
				}
			}

			return errors.New("invalid rate literal")
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
						return errors.New("An object literal explictly declares a property with key '" + k + "' but has the same implicit key")
					}
					return errors.New("duplicate key '" + k + "'")
				}

				keys[k] = isExplicit
			}
		case *SpawnExpression:
			switch n := node.ExprOrVar.(type) {
			case *EmbeddedModule, *Variable, *GlobalVariable:
			case *Call:
				if _, ok := n.Callee.(*IdentifierLiteral); ok {
					break
				}
				return errors.New("invalid spawn expression: the expression should be a global func call, an embedded module or a variable (that can be global)")
			default:
				return errors.New("invalid spawn expression: the expression should be a global func call, an embedded module or a variable (that can be global)")
			}
		case *GlobalConstantDeclarations:
			for _, nameAndValue := range node.NamesValues {
				name := nameAndValue[0].(*IdentifierLiteral).Name

				variables, ok := globalVars[parent]

				if !ok {
					variables = make(map[string]globalVarInfo)
					globalVars[parent] = variables
				}

				_, alreadyUsed := variables[name]
				if alreadyUsed {
					return fmt.Errorf("invalid constant declaration: '%s' is already used", name)
				}
				variables[name] = globalVarInfo{isConst: true}
			}
		case *Assignment, *MultiAssignment:
			var names []string

			switch assignment := n.(type) {
			case *Assignment:

				switch left := assignment.Left.(type) {

				case *GlobalVariable:
					fns, ok := fnDecls[parent]
					if ok {
						_, alreadyUsed := fns[left.Name]
						if alreadyUsed {
							return fmt.Errorf("invalid global variable assignment: '%s' is a declared function's name", left.Name)
						}
					}

					variables, ok := globalVars[parent]

					if !ok {
						variables = make(map[string]globalVarInfo)
						globalVars[parent] = variables
					}

					varInfo, alreadyDefined := variables[left.Name]
					if alreadyDefined {
						if varInfo.isConst {
							return fmt.Errorf("invalid global variable assignment: '%s' is a constant", left.Name)
						}
					} else {
						variables[left.Name] = globalVarInfo{isConst: false}
					}

				case *Variable:
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
				variables, ok := localVars[parent]

				if !ok {
					variables = make(map[string]int)
					localVars[parent] = variables
				}

				variables[name] = 0
			}

		case *FunctionDeclaration:

			switch parent.(type) {
			case *Module, *EmbeddedModule:
				fns, ok := fnDecls[parent]
				vars, globalOk := globalVars[parent]

				if !ok {
					fns = make(map[string]int)
					fnDecls[parent] = fns
				}

				if globalOk {
					_, alreadyUsed := vars[node.Name.Name]
					if alreadyUsed {
						return fmt.Errorf("invalid function declaration: a global variable named '%s' exist", node.Name.Name)
					}
				}

				_, alreadyDeclared := fns[node.Name.Name]
				if alreadyDeclared {
					return fmt.Errorf("invalid function declaration: %s is already declared", node.Name.Name)
				}
				fns[node.Name.Name] = 0
			default:
				return errors.New("invalid function declaration: a function declaration should be a top level statement in a module (embedded or not)")
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
				return fmt.Errorf("invalid break/continue statement: should be in a for statement")
			}

			for i := forStmtIndex + 1; i < len(ancestorChain); i++ {
				switch ancestorChain[i].(type) {
				case *IfStatement, *SwitchStatement, *MatchStatement, *Block:
				default:
					return fmt.Errorf("invalid break/continue statement: should be in a for statement")
				}
			}
		}

		return nil
	})
}

func getQuantity(value float64, unit string) interface{} {
	switch unit {
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

func MustEval(node Node, state *State) interface{} {
	res, err := Eval(node, state)
	if err != nil {
		panic(err)
	}
	return res
}

func Eval(node Node, state *State) (result interface{}, err error) {

	defer func() {
		if e := recover(); e != nil {
			if er, ok := e.(error); ok {
				err = fmt.Errorf("eval: error: %s %s", er, debug.Stack())
			} else {
				err = fmt.Errorf("eval: %s", e)
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
				return nil, errors.New("invalid state")
			}
			return ByteRate(int(qv)), nil
		}

		return nil, errors.New("invalid state")
	case *StringLiteral:
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
	case *PathSlice:
		return n.Value, nil
	case *AbsolutePathExpression:

		pth := ""

		for _, node := range n.Slices {
			pathSlice, err := Eval(node, state)
			if err != nil {
				return nil, err
			}
			switch s := pathSlice.(type) {
			case string:
				pth += s
			default:
				return nil, errors.New("path expression: path slices should have a string value")
			}
		}

		if strings.Contains(pth, "..") {
			return nil, errors.New("path expression: error: result should not contain the substring '..' ")
		}

		if len(pth) >= 2 && pth[1] == '/' {
			pth = pth[1:]
		}

		return Path(pth), nil
	case *URLLiteral:
		return URL(n.Value), nil
	case *HTTPHostLiteral:
		return HTTPHost(n.Value), nil
	case *HTTPHostPatternLiteral:
		return HTTPHostPattern(n.Value), nil
	case *URLPatternLiteral:
		return URLPattern(n.Value), nil
	case *URLExpression:
		pth, err := Eval(n.Path, state)
		if err != nil {
			return nil, err
		}
		return URL(n.HostPart + string(pth.(Path))), nil
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
			list, err := Eval(lhs.Indexed, state)
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

			list.(List)[index.(int)] = right
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
			for _, nameValueNodes := range n.GlobalConstantDeclarations.NamesValues {
				name := nameValueNodes[0].(*IdentifierLiteral).Name
				globalScope[name] = MustEval(nameValueNodes[1], nil)
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
				log.Panicf("invalid key type %T", n)
			}

			obj[k] = v
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
				iterable := val.Interface().(Iterable)
				it := iterable.Iterator()
				index := 0

			iteration:
				for it.HasNext() {
					e := it.GetNext()

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

		for _, switchCase := range n.Cases {
			m, err := Eval(switchCase.Value, state)
			if err != nil {
				return nil, err
			}

			matcher, ok := m.(Matcher)
			if !ok {
				if reflect.TypeOf(m) == reflect.TypeOf(discriminant) { //TODO: change

					if m == discriminant {
						_, err := Eval(switchCase.Block, state)
						if err != nil {
							return nil, err
						}
						break
					} else {
						continue
					}

				}
				return nil, fmt.Errorf("match statement: value of type %T does not implement Matcher interface nor has the same type as value as the discriminant", m)
			}

			if matcher.Test(discriminant) {
				_, err := Eval(switchCase.Block, state)
				if err != nil {
					return nil, err
				}
				break
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
					result = true
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

		res, _, err := memb(left, n.PropertyName.Name)
		return res, err
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
		list, err := Eval(n.Indexed, state)
		if err != nil {
			return nil, err
		}

		l := list.(List)
		var startIndex interface{} = 0
		if n.StartIndex != nil {
			startIndex, err = Eval(n.StartIndex, state)
			if err != nil {
				return nil, err
			}
		}

		var endIndex interface{} = len(l)
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

		return list.(List)[start:end], nil
	case *KeyListExpression:
		list := KeyList{}

		for _, key := range n.Keys {
			list = append(list, string(key.Name))
		}

		return list, nil
	default:
		return nil, fmt.Errorf("cannot evaluate %#v (%T)", node, node)
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
	Entity interface{}
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
	Entity interface{}
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
	HasNext() bool
	GetNext() interface{}
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

type IntRangeIterator struct {
	range_ IntRange
	next   int
}

func (it IntRangeIterator) HasNext() bool {
	if it.range_.inclusiveEnd {
		return it.next <= it.range_.End
	}
	return it.next < it.range_.End
}

func (it *IntRangeIterator) GetNext() interface{} {
	if !it.HasNext() {
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

type ByteCount int
type LineCount int
type ByteRate int

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
}

type waitingJob struct {
	ch        chan struct{}
	need      int64
	use       int64
	abandoned bool
}

// newBucket returns a new token bucket with specified fill interval and
// capability. The bucket is initially full.
func newBucket(interval time.Duration, cap int64, inc int64) *TokenBucket {
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

// Destory destorys the token bucket and stop the inner channels.
func (tb *TokenBucket) Destory() {
	tb.ticker.Stop()
}

func (tb *TokenBucket) adjustDaemon() {
	var waitingJobNow *waitingJob

	for range tb.ticker.C {

		tb.tokenMutex.Lock()

		if tb.avail < tb.cap {
			tb.avail += tb.increment
		}

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
