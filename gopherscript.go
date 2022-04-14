package gopherscript

import (
	"bytes"
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
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

const TRULY_MAX_STACK_HEIGHT = 10
const DEFAULT_MAX_STACK_HEIGHT = 5
const HTTP_URL_PATTERN = "^https?:\\/\\/(localhost|(www\\.)?[-a-zA-Z0-9@:%._+~#=]{1,32}\\.[a-zA-Z0-9]{1,6})\\b([-a-zA-Z0-9@:%_+.~#?&//=]{0,100})$"
const LOOSE_HTTP_EXPR_PATTERN = "^https?:\\/\\/(localhost|(www\\.)?[-a-zA-Z0-9@:%._+~#=]{1,32}\\.[a-zA-Z0-9]{1,6})\\b([-a-zA-Z0-9@:%_+.~#?&//=$]{0,100})$"
const LOOSE_HTTP_HOST_PATTERN_PATTERN = "^https?:\\/\\/(\\*|(www\\.)?[-a-zA-Z0-9.*]{1,32}\\.[a-zA-Z0-9*]{1,6})(:[0-9]{1,5})?$"
const IMPLICIT_KEY_LEN_KEY = "__len"
const GOPHERSCRIPT_MIMETYPE = "application/gopherscript"
const RETURN_1_MODULE_HASH = "SG2a/7YNuwBjsD2OI6bM9jZM4gPcOp9W8g51DrQeyt4="

var HTTP_URL_REGEX = regexp.MustCompile(HTTP_URL_PATTERN)
var LOOSE_HTTP_HOST_PATTERN_REGEX = regexp.MustCompile(LOOSE_HTTP_HOST_PATTERN_PATTERN)
var LOOSE_HTTP_EXPR_PATTERN_REGEX = regexp.MustCompile(LOOSE_HTTP_EXPR_PATTERN)
var isSpace = regexp.MustCompile(`^\s+`).MatchString
var KEYWORDS = []string{"if", "else", "require", "for", "assign", "fn", "switch", "match", "import", "sr"}
var PERMISSION_KIND_STRINGS = []string{"read", "update", "create", "delete", "use", "consume", "provide"}

var CTX_PTR_TYPE = reflect.TypeOf(&Context{})
var ERROR_INTERFACE_TYPE = reflect.TypeOf((*error)(nil)).Elem()
var ITERABLE_INTERFACE_TYPE = reflect.TypeOf((*Iterable)(nil)).Elem()
var MODULE_CACHE = map[string]string{
	RETURN_1_MODULE_HASH: "return 1",
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

func (objLit ObjectLiteral) Permissions(
	globalConsts *GlobalConstantDeclarations,
	runningState *State,
	handleCustomType func(kind PermissionKind, name string) ([]Permission, bool, error),
) []Permission {
	perms := make([]Permission, 0)

	if (globalConsts != nil) && (runningState != nil) {
		log.Panicln("Permissions(): invalid arguments: both arguments cannot be non nil")
	}

	var state *State
	if globalConsts != nil {
		state = NewState(NewContext([]Permission{GlobalVarPermission{ReadPerm, "*"}}))
		globalScope := state.GlobalScope()
		for _, nameValueNodes := range globalConsts.NamesValues {
			globalScope[nameValueNodes[0].(*GlobalVariable).Name] = MustEval(nameValueNodes[1], nil)
		}
	} else {
		state = runningState
	}

	for _, prop := range objLit.Properties {
		name := prop.Name()
		permKind, ok := PermissionKindFromString(name)
		if !ok {
			log.Panicln("invalid requirements, invalid permission kind:", name)
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

						switch gvn := p.Value.(type) {
						case *ListLiteral:
							globalReqNodes = append(globalReqNodes, gvn.Elements...)
						default:
							globalReqNodes = append(globalReqNodes, gvn)
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
	return perms
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
	KeyIndexVar   *Variable //can be nil
	ValueElemVar  *Variable //can be nil
	Body          *Block
	IteratedValue Node
}

type Block struct {
	NodeBase
	Statements []Node
}

type ReturnStatement struct {
	NodeBase
	Expr Node
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
	Keyof
	Dot //unused, present for symmetry
	Range
	ExclEndRange
)

var BINARY_OPERATOR_STRINGS = []string{
	"+", "+.", "-", "-.", "*", "*.", "/", "/.", "++", "<", "<.", "<=", "<=", ">", ">.", ">=", ">=.", "==", "!=",
	"in", "keyof", ".", "..", "..<",
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
	Var *Variable
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

//special int types
type LineCount int

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
	IsMatcherFor(interface{}) bool
}

func (patt PathPattern) IsMatcherFor(v interface{}) bool {
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

func (patt HTTPHostPattern) IsMatcherFor(v interface{}) bool {
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

func (patt URLPattern) IsMatcherFor(v interface{}) bool {
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
	var err error

	//we first get the callee
	switch c := calleeNode.(type) {
	case *IdentifierLiteral:
		err := state.ctx.CheckHasPermission(GlobalVarPermission{Kind_: UsePerm, Name: c.Name})
		if err != nil {
			return nil, err
		}
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
			v, err = memb(v, idents.Name)
			if err != nil {
				return nil, err
			}
		}
		callee = v
	case *Variable, *MemberExpression:
		callee, err = Eval(calleeNode, state)
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

		if fnValType.NumIn() == 0 || !CTX_PTR_TYPE.AssignableTo(fnValType.In(0)) {
			log.Panicln("cannot call a function whose first parameter is not a Context")
		}

		var ctx *Context = state.ctx
		if isExt {
			ctx = extState.ctx
		}

		args = append(List{ctx}, args...)

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
		})
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

	eatSpaceAndNewLineAndSemiColonAndComment := func() {
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

	eatSpaceAndNewlineAndComma := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == ',') {
			i++
		}
	}

	eatSpaceAndComma := func() {
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
			eatSpaceAndNewLineAndSemiColonAndComment()

			if i < len(s) && s[i] == '}' {
				break
			}

			stmts = append(stmts, parseStatement())
			eatSpaceAndNewLineAndSemiColonAndComment()
		}

		if i >= len(s) {
			panic(ParsingError{"unterminated block, missing closing brace '}", i, openingBraceIndex})
		}

		if s[i] != '}' {
			panic(ParsingError{"invalid block", i, openingBraceIndex})
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
					})
				}

				if strings.HasSuffix(value, "/...") {
					panic(ParsingError{
						"prefix path patterns cannot contain globbing patterns '" + value + "'",
						i,
						start,
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
			})
		}

		if strings.Contains(_url, "?") {
			panic(ParsingError{
				"URLs with a query parts are not supported yet'" + _url,
				i,
				start,
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
						})
					}
				} else {
					replaced := strings.ReplaceAll(_url, "*", "com")
					if _, err := url.Parse(replaced); err != nil {

						panic(ParsingError{
							"invalid HTTP host pattern '" + _url + "' : " + err.Error(),
							i,
							start,
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
					})
				}

				if isPrefixPattern {
					panic(ParsingError{
						"an URL expression cannot ends with /...",
						i,
						start,
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
			})
		}

		parsed, err := url.Parse(_url)
		if err != nil {
			panic(ParsingError{
				"invalid URL '" + _url + "'",
				i,
				start,
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
					})
				}

				if !isAlpha(s[i]) {
					panic(ParsingError{
						"property name should start with a letter not '" + string(s[i]) + "'",
						i,
						start,
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
			})
		}

		return ident
	}

	parseKeyList := func() *KeyListExpression {
		start := i
		i += 2

		var idents []*IdentifierLiteral

		for i < len(s) && s[i] != '}' {
			eatSpaceAndComma()

			if i >= len(s) {
				panic(ParsingError{
					"unterminated key list, missing closing brace '}'",
					i,
					start,
				})
			}

			if ident, ok := parseExpression().(*IdentifierLiteral); ok {
				idents = append(idents, ident)
			} else {
				panic(ParsingError{
					"a key list can only contain identifiers",
					i,
					start,
				})
			}

			eatSpaceAndComma()
		}

		if i >= len(s) {
			panic(ParsingError{
				"unterminated key list, missing closing brace '}'",
				i,
				start,
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

					eatSpaceAndNewLineAndSemiColonAndComment()

					for i < len(s) && s[i] != '}' {
						stmts = append(stmts, parseStatement())
						eatSpaceAndNewLineAndSemiColonAndComment()
					}

					if i >= len(s) || s[i] != '}' {
						panic(ParsingError{
							"unterminated embedded module",
							i,
							start,
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
			case s[i] == '"':
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
			case s[i] == '(' && !isKeyword(name):
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
					eatSpaceAndNewlineAndComma()
					arg := parseExpression()

					call.Arguments = append(call.Arguments, arg)
					eatSpaceAndNewlineAndComma()
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
			case s[i] == '$':
				i++
				if i >= len(s) || (s[i] != '\t' && s[i] != ' ') {
					panic(ParsingError{
						"a non-parenthesized call expression should have arguments and the callee (<name>$) should be followed by a space",
						i,
						identLike.Base().Span.Start,
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

				for i < len(s) && s[i] != '\n' && !isDelim(s[i]) {
					eatSpaceAndComments()

					if s[i] == '\n' || isDelim(s[i]) {
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
			}

			return literal

		case '{': //object
			openingBraceIndex := i
			i++

			unamedPropCount := 0
			var properties []ObjectProperty

			for i < len(s) && s[i] != '}' {
				eatSpaceAndNewlineAndComma()

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
							})
						}

						if singleKey {
							if s[i] != ':' {
								panic(ParsingError{
									"invalid object literal, following key should be followed by a colon : '" + lastKeyName + "'",
									i,
									openingBraceIndex,
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

				eatSpaceAndNewlineAndComma()
			}

			if i >= len(s) {
				panic(ParsingError{
					"unterminated object literal, missing closing brace '}'",
					i,
					openingBraceIndex,
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
				eatSpaceAndNewlineAndComma()

				if i < len(s) && s[i] == ']' {
					break
				}

				e := parseExpression()
				elements = append(elements, e)

				eatSpaceAndNewlineAndComma()
			}

			if i >= len(s) || s[i] != ']' {
				panic(ParsingError{
					"unterminated list literal, missing closing bracket ']'",
					i,
					openingBracketIndex,
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
				})
			case '=':
				i++
				if i >= len(s) {
					panic(ParsingError{
						NON_EXISTING_OPERATOR,
						i,
						openingParenIndex,
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
				})
			case 'i':
				i++
				if i >= len(s) {
					panic(ParsingError{
						UNTERMINATED_BIN_EXPR,
						i,
						openingParenIndex,
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
				})
			}

			right := parseExpression()

			eatSpace()
			if i >= len(s) {
				panic(ParsingError{
					UNTERMINATED_BIN_EXPR + " missing closing parenthesis",
					i,
					openingParenIndex,
				})
			}

			if s[i] != ')' {
				panic(ParsingError{
					"invalid binary expression",
					i,
					openingParenIndex,
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
					})
				}

				if s[i-1] == '[' {
					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated index expression",
							i,
							first.Base().Span.Start,
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
						})
					}

					if s[i] == ':' {
						if isSliceExpr {
							panic(ParsingError{
								"invalid slice expression, a single colon should be present",
								i,
								first.Base().Span.Start,
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
					if !isAlpha(s[i]) {
						panic(ParsingError{
							"property name should start with a letter not '" + string(s[i]) + "'",
							i,
							first.Base().Span.Start,
						})
					}

					for i < len(s) && isAlpha(s[i]) {
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
				eatSpaceAndNewlineAndComma()

				if i >= len(s) || s[i] == ')' {
					break
				}

				arg := parseExpression()

				call.Arguments = append(call.Arguments, arg)
				eatSpaceAndNewlineAndComma()
			}

			if i >= len(s) || s[i] != ')' {
				panic(ParsingError{
					"unterminated call, missing closing parenthesis ')'",
					i,
					first.Base().Span.Start,
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
				})
			}

			if s[i] != '(' {
				panic(ParsingError{
					"invalid global const declarations, expected opening parenthesis after 'const'",
					i,
					start,
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
					})
				}

				lhs := parseExpression()
				globvar, ok := lhs.(*GlobalVariable)
				if !ok {
					panic(ParsingError{
						"invalid global const declarations, left hand sides must be global variable identifiers",
						i,
						start,
					})
				}

				eatSpace()

				if i >= len(s) || s[i] != '=' {
					panic(ParsingError{
						fmt.Sprintf("invalid global const declarations, missing '=' after name %s", globvar.Name),
						i,
						start,
					})
				}

				i++
				eatSpace()

				if i >= len(s) || s[i] == ')' {
					panic(ParsingError{
						fmt.Sprintf("invalid global const declarations, missing value after '$$%s ='", globvar.Name),
						i,
						start,
					})
				}

				rhs := parseExpression()
				if !isSimpleValueLiteral(rhs) {
					panic(ParsingError{
						fmt.Sprintf("invalid global const declarations, only literals are allowed as values : %T", rhs),
						i,
						start,
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
				})
			}
		}

		if i >= len(s) || s[i] != '(' {
			panic(ParsingError{
				"function : fn keyword (or function name) should be followed by '(' <param list> ')' ",
				i,
				start,
			})
		}

		i++

		var parameters []FunctionParameter

		for i < len(s) && s[i] != ')' {
			eatSpaceAndNewlineAndComma()

			if i < len(s) && s[i] == ')' {
				break
			}

			varNode := parseExpression()

			if _, ok := varNode.(*Variable); !ok {
				panic(ParsingError{
					"function : the parameter list should contain variables separated by a comma",
					i,
					start,
				})
			}

			parameters = append(parameters, FunctionParameter{
				Var: varNode.(*Variable),
			})

			eatSpaceAndNewlineAndComma()
		}

		if i >= len(s) {
			panic(ParsingError{
				"function : unterminated parameter list : missing closing parenthesis",
				i,
				start,
			})
		}

		if s[i] != ')' {
			panic(ParsingError{
				"function : invalid syntax",
				i,
				start,
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
					})
				}

				if s[i] != '{' {
					panic(ParsingError{
						"invalid if statement, test expression should be followed by a block, not " + string(s[i]),
						i,
						expr.Base().Span.Start,
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
						})
					}

					if s[i] != '{' {
						panic(ParsingError{
							"invalid if statement, else should be followed by a block, not " + string(s[i]),
							i,
							expr.Base().Span.Start,
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
				keyIndexVar := parseExpression()

				switch v := keyIndexVar.(type) {
				case *Variable:
					eatSpace()

					if i > len(s) {
						panic(ParsingError{
							"invalid for statement",
							i,
							forStart,
						})
					}

					if s[i] != ',' {
						panic(ParsingError{
							"for statement : key/index variale should be followed by a comma ',' , not " + string(s[i]),
							i,
							forStart,
						})
					}

					i++
					eatSpace()

					if i > len(s) {
						panic(ParsingError{
							"unterminated for statement",
							i,
							forStart,
						})
					}

					valueElemVar := parseExpression()

					if _, isVar := valueElemVar.(*Variable); !isVar {
						panic(ParsingError{
							fmt.Sprintf("invalid for statement : 'for <key-index var> <colon> should be followed by a variable, not a(n) %T", keyIndexVar),
							i,
							forStart,
						})
					}

					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated for statement",
							i,
							forStart,
						})
					}

					if s[i] != 'i' || i > len(s)-2 || s[i+1] != 'n' {
						panic(ParsingError{
							"invalid for statement : missing 'in' keyword ",
							i,
							forStart,
						})
					}

					i += 2

					if i < len(s) && s[i] != ' ' {
						panic(ParsingError{
							"invalid for statement : 'in' keyword should be followed by a space",
							i,
							forStart,
						})
					}
					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated for statement, missing value after 'in'",
							i,
							forStart,
						})
					}

					iteratedValue := parseExpression()

					eatSpace()

					if i >= len(s) {
						panic(ParsingError{
							"unterminated for statement, missing block",
							i,
							forStart,
						})
					}

					blk := parseBlock()

					return &ForStatement{
						NodeBase: NodeBase{
							Span: NodeSpan{ev.Span.Start, blk.Span.End},
						},
						KeyIndexVar:   keyIndexVar.(*Variable),
						ValueElemVar:  valueElemVar.(*Variable),
						Body:          blk,
						IteratedValue: iteratedValue,
					}
				case *BinaryExpression:
					if v.Operator == Range || v.Operator == ExclEndRange {
						iteratedValue := keyIndexVar
						keyIndexVar = nil

						eatSpace()

						if i >= len(s) {
							panic(ParsingError{
								"unterminated for statement, missing block",
								i,
								forStart,
							})
						}

						blk := parseBlock()

						return &ForStatement{
							NodeBase: NodeBase{
								Span: NodeSpan{ev.Span.Start, blk.Span.End},
							},
							KeyIndexVar:   nil,
							ValueElemVar:  nil,
							Body:          blk,
							IteratedValue: iteratedValue,
						}
					}
					panic(ParsingError{
						fmt.Sprintf("invalid for statement : 'for' should be followed by a binary range expression, operator is %s", v.Operator.String()),
						i,
						forStart,
					})
				default:
					panic(ParsingError{
						fmt.Sprintf("invalid for statement : 'for' should be followed by a variable or a binary range expression (binary range operator), not a(n) %T", keyIndexVar),
						i,
						forStart,
					})
				}

			case "switch", "match":
				switchMatchStart := expr.Base().Span.Start

				eatSpace()

				if i >= len(s) {
					panic(ParsingError{
						"unterminated switch statement: missing value",
						i,
						switchMatchStart,
					})
				}

				discriminant := parseExpression()
				var switchCases []*Case

				eatSpace()

				if i >= len(s) || s[i] != '{' {
					panic(ParsingError{
						"unterminated switch statement : missing body",
						i,
						switchMatchStart,
					})
				}

				i++

				for i < len(s) && s[i] != '}' {
					eatSpaceAndNewLineAndSemiColonAndComment()

					var valueNodes []Node

					for i < len(s) && s[i] != '{' {
						if i >= len(s) {
							panic(ParsingError{
								"unterminated switch statement",
								i,
								switchMatchStart,
							})
						}
						valueNode := parseExpression()
						if !isSimpleValueLiteral(valueNode) {
							panic(ParsingError{
								"invalid switch/match case : only simple value literals are supported (1, 1.0, /home, ..)",
								i,
								switchMatchStart,
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
						panic(ParsingError{
							"invalid switch case : missing block",
							i,
							switchMatchStart,
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

					eatSpaceAndNewLineAndSemiColonAndComment()
				}

				if i >= len(s) || s[i] != '}' {
					panic(ParsingError{
						"unterminated switch statement : missing closing body brace '}'",
						i,
						switchMatchStart,
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
					})

				}

				eatSpace()

				url_ := parseExpression()
				if _, ok := url_.(*URLLiteral); !ok {
					panic(ParsingError{
						"import statement: URL should be a URL literal",
						i,
						importStart,
					})
				}

				eatSpace()

				checksum := parseExpression()
				if _, ok := checksum.(*StringLiteral); !ok {
					panic(ParsingError{
						"import statement: checksum should be a string literal",
						i,
						importStart,
					})
				}

				eatSpace()

				argumentObject := parseExpression()
				if _, ok := argumentObject.(*ObjectLiteral); !ok {
					panic(ParsingError{
						"import statement: argument should be an object literal",
						i,
						importStart,
					})
				}

				eatSpace()
				allowIdent := parseExpression()
				if ident, ok := allowIdent.(*IdentifierLiteral); !ok || ident.Name != "allow" {
					panic(ParsingError{
						"import statement: argument should be followed by a the 'allow' keyword",
						i,
						importStart,
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
			case "assign":
				var vars []Node

				for i < len(s) && s[i] != '=' {
					eatSpace()
					e := parseExpression()
					if _, ok := e.(*Variable); !ok {
						panic(ParsingError{
							"assign keyword should be followed by variables (assign $a $b = <value>)",
							i,
							expr.Base().Span.Start,
						})
					}
					vars = append(vars, e)
					eatSpace()

				}

				if i >= len(s) {
					panic(ParsingError{
						"unterminated assign statement, missing '='",
						i,
						expr.Base().Span.Start,
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
				if !followedBySpace || s[i] == '\n' || (isDelim(s[i]) && s[i] != '(') {
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

				for i < len(s) && s[i] != '\n' && !isDelim(s[i]) {
					eatSpaceAndComments()

					if s[i] == '\n' || isDelim(s[i]) {
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

	eatSpaceAndNewLineAndSemiColonAndComment()
	globalConstDecls := parseGlobalConstantDeclarations()

	eatSpaceAndNewLineAndSemiColonAndComment()
	requirements := parseRequirements()

	eatSpaceAndNewLineAndSemiColonAndComment()

	for i < len(s) {
		stmts = append(stmts, parseStatement())
		eatSpaceAndNewLineAndSemiColonAndComment()
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

func isGopherVal(v interface{}) bool {
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
	if isGopherVal(v) {
		return v
	}
	switch val := v.(type) {
	case reflect.Value:
		intf := val.Interface()
		if isGopherVal(intf) {
			return intf
		}
		return val
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

type Context struct {
	grantedPermissions []Permission
	stackPermission    StackPermission
}

func NewContext(permissions []Permission) *Context {

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

	return &Context{
		grantedPermissions: permissions,
		stackPermission:    stackPermission,
	}
}

func (ctx *Context) HasPermission(perm Permission) bool {
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

//Creates a new Context  with the permissions passed as argument removed.
func (ctx *Context) Without(removedPerms []Permission) (*Context, error) {

	var perms []Permission

top:
	for _, perm := range ctx.grantedPermissions {
		for _, removedPerm := range removedPerms {
			if removedPerm.Includes(perm) {
				continue top
			}
			if perm.Includes(removedPerm) {
				return nil, fmt.Errorf("cannot created new context with removed permission %s", removedPerm.String())
			}
		}
		perms = append(perms, perm)
	}

	return NewContext(perms), nil
}

type State struct {
	ScopeStack  []map[string]interface{}
	ReturnValue *interface{}
	ctx         *Context
	constants   map[string]int
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

func memb(value interface{}, name string) (interface{}, error) {
	switch v := value.(type) {
	case Object:
		return v[name], nil
	case ExternalValue:
		if obj, ok := v.value.(Object); !ok {
			return nil, errors.New("member expression: external value: only objects supported")
		} else {
			return ExtValOf(obj[name], v.state), nil
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
				return ValOf(fieldValue), nil
			}
			fallthrough
		case reflect.Interface:
			method := v.MethodByName(name)
			if !method.IsValid() {
				if ptr.IsValid() {
					method = ptr.MethodByName(name)
				}
				if !method.IsValid() {
					return nil, errors.New("property ." + name + " does not exist")
				}
			}
			return method, nil
		default:
			return nil, errors.New("Cannot get property ." + name + " for a value of kind " + v.Kind().String())
		}

	default:
		return nil, errors.New("cannot get property of non object/Go value")
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
		state.ctx = NewContext(nil)
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

func Walk(node, parent Node, fn func(Node, Node) error) error {
	//refactor with panics ?

	if err := fn(node, parent); err != nil {
		return err
	}

	switch n := node.(type) {
	case *Module:
		if n.Requirements != nil {
			if err := Walk(n.Requirements.Object, node, fn); err != nil {
				return err
			}
		}

		if n.GlobalConstantDeclarations != nil {
			if err := Walk(n.GlobalConstantDeclarations, node, fn); err != nil {
				return err
			}
		}

		for _, stmt := range n.Statements {
			if err := Walk(stmt, node, fn); err != nil {
				return err
			}
		}
	case *EmbeddedModule:
		if n.Requirements != nil {
			if err := Walk(n.Requirements.Object, node, fn); err != nil {
				return err
			}
		}

		for _, stmt := range n.Statements {
			if err := Walk(stmt, node, fn); err != nil {
				return err
			}
		}
	case *ImportStatement:
		if err := Walk(n.Identifier, node, fn); err != nil {
			return err
		}
		if err := Walk(n.URL, node, fn); err != nil {
			return err
		}
		if err := Walk(n.ValidationString, node, fn); err != nil {
			return err
		}
		if err := Walk(n.ArgumentObject, node, fn); err != nil {
			return err
		}
		if err := Walk(n.GrantedPermissions, node, fn); err != nil {
			return err
		}
	case *SpawnExpression:
		if n.GroupIdent != nil {
			if err := Walk(n.GroupIdent, node, fn); err != nil {
				return err
			}
		}
		if err := Walk(n.Globals, node, fn); err != nil {
			return err
		}
		if err := Walk(n.ExprOrVar, node, fn); err != nil {
			return err
		}
		if n.GrantedPermissions != nil {
			if err := Walk(n.GrantedPermissions, node, fn); err != nil {
				return err
			}
		}
	case *Block:
		for _, stmt := range n.Statements {
			if err := Walk(stmt, node, fn); err != nil {
				return err
			}
		}
	case *FunctionDeclaration:
		if err := Walk(n.Name, node, fn); err != nil {
			return err
		}
		if err := Walk(n.Function, node, fn); err != nil {
			return err
		}
	case *FunctionExpression:
		for _, p := range n.Parameters {
			if err := Walk(p.Var, node, fn); err != nil {
				return err
			}
		}
		if err := Walk(n.Body, node, fn); err != nil {
			return err
		}

		if n.Requirements != nil {
			if err := Walk(n.Requirements.Object, node, fn); err != nil {
				return err
			}
		}

	case *ObjectLiteral:
		for _, prop := range n.Properties {
			if err := Walk(&prop, node, fn); err != nil {
				return err
			}
		}
	case *ObjectProperty:
		if n.Key != nil {
			if err := Walk(n.Key, node, fn); err != nil {
				return err
			}
		}

		if err := Walk(n.Value, node, fn); err != nil {
			return err
		}
	case *MemberExpression:
		if err := Walk(n.Left, node, fn); err != nil {
			return err
		}
		if err := Walk(n.PropertyName, node, fn); err != nil {
			return err
		}
	case *IndexExpression:
		if err := Walk(n.Indexed, node, fn); err != nil {
			return err
		}
		if err := Walk(n.Index, node, fn); err != nil {
			return err
		}
	case *SliceExpression:
		if err := Walk(n.Indexed, node, fn); err != nil {
			return err
		}
		if n.StartIndex != nil {
			if err := Walk(n.StartIndex, node, fn); err != nil {
				return err
			}
		}
		if n.EndIndex != nil {
			if err := Walk(n.EndIndex, node, fn); err != nil {
				return err
			}
		}
	case *IdentifierMemberExpression:
		if err := Walk(n.Left, node, fn); err != nil {
			return err
		}
		for _, p := range n.PropertyNames {
			if err := Walk(p, node, fn); err != nil {
				return err
			}
		}
	case *KeyListExpression:
		for _, key := range n.Keys {
			if err := Walk(key, node, fn); err != nil {
				return err
			}
		}
	case *Assignment:
		if err := Walk(n.Left, node, fn); err != nil {
			return err
		}
		if err := Walk(n.Right, node, fn); err != nil {
			return err
		}
	case *MultiAssignment:
		for _, vr := range n.Variables {
			if err := Walk(vr, node, fn); err != nil {
				return err
			}
		}
		if err := Walk(n.Right, node, fn); err != nil {
			return err
		}
	case *Call:
		Walk(n.Callee, node, fn)
		for _, arg := range n.Arguments {
			if err := Walk(arg, node, fn); err != nil {
				return err
			}
		}
	case *IfStatement:
		if err := Walk(n.Test, node, fn); err != nil {
			return err
		}
		if err := Walk(n.Consequent, node, fn); err != nil {
			return err
		}
		if n.Alternate != nil {
			if err := Walk(n.Alternate, node, fn); err != nil {
				return err
			}
		}
	case *ForStatement:
		if n.KeyIndexVar != nil {
			if err := Walk(n.KeyIndexVar, node, fn); err != nil {
				return err
			}
			if err := Walk(n.ValueElemVar, node, fn); err != nil {
				return err
			}
		}

		if err := Walk(n.IteratedValue, node, fn); err != nil {
			return err
		}
		if err := Walk(n.Body, node, fn); err != nil {
			return err
		}
	case *ReturnStatement:
		if err := Walk(n.Expr, node, fn); err != nil {
			return err
		}
	case *SwitchStatement:
		if err := Walk(n.Discriminant, node, fn); err != nil {
			return err
		}
		for _, switcCase := range n.Cases {
			if err := Walk(switcCase, node, fn); err != nil {
				return err
			}
		}
	case *MatchStatement:
		if err := Walk(n.Discriminant, node, fn); err != nil {
			return err
		}
		for _, switcCase := range n.Cases {
			if err := Walk(switcCase, node, fn); err != nil {
				return err
			}
		}
	case *Case:
		if err := Walk(n.Value, node, fn); err != nil {
			return err
		}
		if err := Walk(n.Block, node, fn); err != nil {
			return err
		}
	case *LazyExpression:
		if err := Walk(n.Expression, node, fn); err != nil {
			return err
		}
	case *BinaryExpression:
		if err := Walk(n.Left, node, fn); err != nil {
			return err
		}
		if err := Walk(n.Right, node, fn); err != nil {
			return err
		}
	case *UpperBoundRangeExpression:
		if err := Walk(n.UpperBound, node, fn); err != nil {
			return err
		}
	case *AbsolutePathExpression:
		for _, e := range n.Slices {
			if err := Walk(e, node, fn); err != nil {
				return err
			}
		}
	case *RelativePathExpression:
		for _, e := range n.Slices {
			if err := Walk(e, node, fn); err != nil {
				return err
			}
		}
	case *URLExpression:
		if err := Walk(n.Path, node, fn); err != nil {
			return err
		}
	}

	return nil
}

func Check(node Node) error {

	//key: *Module|*EmbeddedModule
	fnDecls := make(map[Node]map[string]int)

	return Walk(node, nil, func(n Node, parent Node) error {

		switch node := n.(type) {
		case *QuantityLiteral:
			switch node.Unit {
			case "s", "ms", "%", "ln":
			default:
				return errors.New("non supported unit: " + node.Unit)
			}
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
		case *FunctionDeclaration:

			switch parent.(type) {
			case *Module, *EmbeddedModule:
				fns, ok := fnDecls[parent]
				if !ok {
					fns = make(map[string]int)
					fnDecls[parent] = fns
				}

				_, alreadyDeclared := fns[node.Name.Name]
				if alreadyDeclared {
					return fmt.Errorf("invalid function declaration: %s is already declared", node.Name.Name)
				}
				fns[node.Name.Name] = 0
			default:
				return errors.New("invalid function declaration: a function declaration should be a top level statement in a module (embedded or not)")
			}
		}

		return nil
	})
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
		switch n.Unit {
		case "s":
			return reflect.ValueOf(time.Duration(n.Value) * time.Second), nil
		case "ms":
			return reflect.ValueOf(time.Duration(n.Value) * time.Millisecond), nil
		case "%":
			return n.Value / 100, nil
		case "ln":
			return LineCount(int(n.Value)), nil
		default:
			panic("unsupported unit " + n.Unit)
		}
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

			keyValue := reflect.ValueOf(var_.(*Variable).Name)
			scopeValue.SetMapIndex(keyValue, elemValue)
		}

		return nil, nil
	case *Module:
		state.ScopeStack = state.ScopeStack[:1] //we only keep the global scope
		state.PushScope()
		state.ReturnValue = nil
		defer func() {
			state.ReturnValue = nil
			state.PopScope()
		}()

		//CONSTANTS
		if n.GlobalConstantDeclarations != nil {
			globalScope := state.GlobalScope()
			for _, nameValueNodes := range n.GlobalConstantDeclarations.NamesValues {
				name := nameValueNodes[0].(*GlobalVariable).Name
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
		for _, stmt := range n.Statements {
			_, err := Eval(stmt, state)
			if err != nil {
				return nil, err
			}

			if state.ReturnValue != nil {
				return nil, nil
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

		perms := n.GrantedPermissions.Permissions(nil, nil, nil)
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

		routine, err := spawnRoutine(state, globals, mod, NewContext(perms))
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
				newCtx, err := state.ctx.Without([]Permission{
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
			perms := n.GrantedPermissions.Permissions(nil, state, nil)
			for _, perm := range perms {
				if err := state.ctx.CheckHasPermission(perm); err != nil {
					return nil, fmt.Errorf("spawn: cannot allow permission: %s", err.Error())
				}
			}
			ctx = NewContext(perms)
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

		if n.KeyIndexVar != nil {
			kVarname = n.KeyIndexVar.Name
			eVarname = n.ValueElemVar.Name
		}

		defer func() {
			if n.KeyIndexVar != nil {
				state.CurrentScope()[kVarname] = nil
				state.CurrentScope()[eVarname] = nil
			}
		}()

		switch v := iteratedValue.(type) {
		case Object:
			for k, v := range v {
				if n.KeyIndexVar != nil {
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
			}
		case List:
			for i, e := range v {
				if n.KeyIndexVar != nil {

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
			}
		default:
			val := ToReflectVal(v)

			if val.IsValid() && val.Type().Implements(ITERABLE_INTERFACE_TYPE) {
				iterable := val.Interface().(Iterable)
				it := iterable.Iterator()
				index := 0

				for it.HasNext() {
					e := it.GetNext()

					if n.KeyIndexVar != nil {
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

			if matcher.IsMatcherFor(discriminant) {
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

		return memb(left, n.PropertyName.Name)
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
		return e.IsMatcherFor(otherFsPerm.Entity)
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
		return e.IsMatcherFor(otherHttpPerm.Entity)
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
		return e.IsMatcherFor(otherHttpPerm.Entity)
	}

	return false
}

func (perm HttpPermission) String() string {
	return fmt.Sprintf("[%s %s]", perm.Kind_, perm.Entity)
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
