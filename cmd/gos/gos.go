package main

import (
	//STANDARD LIBRARY
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gopherscript "github.com/debloat-dev/Gopherscript"

	//EXTERNAL
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

const BACKSPACE_CODE = 8
const DEL_CODE = 127
const CTRL_BACKSPACE_CODE = 23
const ENTER_CODE = 13
const CTRL_C_CODE = 3
const TAB_CODE = 9
const ESCAPE_CODE = 27

const DEFAULT_FILE_FMODE = fs.FileMode(0o400)
const DEFAULT_RW_FILE_FMODE = fs.FileMode(0o600)
const DEFAULT_DIR_FMODE = fs.FileMode(0o500)
const DEFAULT_HTTP_CLIENT_TIMEOUT = 10 * time.Second
const KV_STORE_PERSISTENCE_INTERVAL = 100 * time.Millisecond

const PATH_ARG_PROVIDED_TWICE = "path argument provided at least twice"
const CONTENT_ARG_PROVIDED_TWICE = "content argument provided at least twice"
const MISSING_URL_ARG = "missing URL argument"
const HTTP_OPTION_OBJECT_PROVIDED_TWICE = "http option object provided at least twice"

const JSON_CTYPE = "application/json"
const HTML_CTYPE = "text/html"
const PLAIN_TEXT_CTYPE = "text/plain"

const FS_WRITE_LIMIT_NAME = "fs/write"
const FS_READ_LIMIT_NAME = "fs/read"
const FS_TOTAL_NEW_FILE_LIMIT_NAME = "fs/total-new-file"
const FS_NEW_FILE_RATE_LIMIT_NAME = "fs/new-file"

const HTTP_REQUEST_RATE_LIMIT_NAME = "http/request"

const FS_WRITE_MIN_CHUNK_SIZE = 100_000
const FS_READ_MIN_CHUNK_SIZE = 1_000_000

const CONTROL_KEYWORD_COLOR = termenv.ANSIBrightMagenta
const OTHER_KEYWORD_COLOR = termenv.ANSIBlue

var DEFAULT_HTTP_REQUEST_OPTIONS = &httpRequestOptions{
	timeout:            DEFAULT_HTTP_CLIENT_TIMEOUT,
	InsecureSkipVerify: true,
}

func writePrompt(state *gopherscript.State, config REPLConfiguration) (prompt_length int) {

	for _, part := range config.prompt {

		color := config.defaultFgColor

		list, isList := part.(gopherscript.List)

		if isList && len(list) == 2 {
			part = list[0]
			colorIdent, isIdent := list[1].(gopherscript.Identifier)

			if isIdent {
				clr, ok := COLOR_NAME_TO_COLOR[colorIdent]
				if ok {
					color = clr
				}
			}
		}

		s := ""

		switch p := part.(type) {
		case string:
			s = p
		case *gopherscript.LazyExpression:
			if !gopherscript.IsSimpleValueLiteral(p.Expression) && !gopherscript.Is(p.Expression, (*gopherscript.URLExpression)(nil)) {
				panic(fmt.Errorf("writePrompt: only url expressions and simple-value literals can be evaluated"))
			}
			v, _ := gopherscript.Eval(p.Expression, state)
			s = fmt.Sprintf("%s", v)
		default:
		}

		//we print the part
		prompt_length += len([]rune(s))
		styled := termenv.String(s)
		styled = styled.Foreground(color)
		fmt.Print(styled)
	}
	return
}

var COLOR_NAME_TO_COLOR = map[gopherscript.Identifier]termenv.Color{
	"red":        termenv.ANSIRed,
	"bright-red": termenv.ANSIBrightRed,

	"blue":        termenv.ANSIBlue,
	"bright-blue": termenv.ANSIBrightBlue,

	"cyan":        termenv.ANSICyan,
	"bright-cyan": termenv.ANSIBrightCyan,

	"yellow":        termenv.ANSIYellow,
	"bright-yellow": termenv.ANSIBrightYellow,

	"green":        termenv.ANSIGreen,
	"bright-green": termenv.ANSIBrightGreen,

	"white":        termenv.ANSIWhite,
	"bright-white": termenv.ANSIBrightWhite,

	"black":        termenv.ANSIBlack,
	"bright-black": termenv.ANSIBrightBlack,

	"magenta":        termenv.ANSIMagenta,
	"bright-magenta": termenv.ANSIBrightMagenta,
}

func replaceNewLinesRawMode(s string) string {
	return strings.ReplaceAll(s, "\n", "\n\x1b[1E")
}

func getClosingDelimiter(openingDelim rune) rune {
	switch openingDelim {
	case '[':
		return ']'
	case '{':
		return '}'
	case '(':
		return ')'
	default:
		return openingDelim
	}
}

func strSliceContains(strings []string, str string) bool {
	for _, e := range strings {
		if e == str {
			return true
		}
	}

	return false
}

type SpecialCode int

const (
	NotSpecialCode SpecialCode = iota
	ArrowUp
	ArrowDown
	ArrowRight
	ArrowLeft
	End
	Home
	Backspace
	CtrlBackspace
	Enter
	CtrlC
	Tab
	Escape
	EscapeNext
)

func (code SpecialCode) String() string {
	mp := map[SpecialCode]string{
		NotSpecialCode: "NotSpecialCode",
		ArrowUp:        "ArrowUp",
		ArrowDown:      "ArrowDown",
		ArrowLeft:      "ArrowLeft",
		End:            "end",
		Home:           "Home",
		Backspace:      "Backspace",
		CtrlBackspace:  "CtrlBackspace",
		Enter:          "Enter",
		CtrlC:          "CtrlC",
		Tab:            "Tab",
		Escape:         "Escape",
		EscapeNext:     "EscapeNext",
	}
	return mp[code]
}

func getSpecialCode(runeSlice []rune) SpecialCode {

	if len(runeSlice) == 1 {
		switch runeSlice[0] {
		case DEL_CODE, BACKSPACE_CODE:
			return Backspace
		case CTRL_BACKSPACE_CODE:
			return CtrlBackspace
		case ENTER_CODE:
			return Enter
		case CTRL_C_CODE:
			return CtrlC
		case TAB_CODE:
			return Tab
		case ESCAPE_CODE:
			return Escape
		}
	}

	if len(runeSlice) >= 2 && runeSlice[0] == 27 && runeSlice[1] == 91 {

		if len(runeSlice) == 2 {
			return EscapeNext
		}
		if len(runeSlice) == 3 {
			switch runeSlice[2] {
			case 65:
				return ArrowUp
			case 66:
				return ArrowDown
			case 67:
				return ArrowRight
			case 68:
				return ArrowLeft
			case 54:
				return End
			case 72:
				return Home
			}
		}
	}

	return NotSpecialCode
}

type CommandHistory struct {
	Commands []string `json:"commands"`
}

type ColorizationInfo struct {
	span  gopherscript.NodeSpan
	color termenv.Color
}

func debug(args ...interface{}) {
	f, err := os.OpenFile(".debug.txt", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		log.Panicln(err)
	}

	_, err = f.Write([]byte(fmt.Sprint(args...)))
	if err != nil {
		log.Panicln(err)
	}

	_, err = f.WriteString("\n")
	if err != nil {
		log.Panicln(err)
	}

	f.Close()
}

func copyRuneSlice(src []rune) []rune {
	var sliceCopy = make([]rune, len(src))

	copy(sliceCopy, src)
	return sliceCopy
}

func startShell(state *gopherscript.State, ctx *gopherscript.Context, config REPLConfiguration) {
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic(err)
	}
	defer term.Restore(int(os.Stdin.Fd()), old)

	reader := bufio.NewReader(os.Stdin)

	history := CommandHistory{Commands: []string{""}}
	commandIndex := 0

	input := make([]rune, 0)
	var runeSeq []rune
	backspaceCount := 0
	pressedTabCount := 0
	ignoreNextChar := false
	promptLen := writePrompt(state, config)

	reset := func() {
		input = nil
		backspaceCount = 0
		runeSeq = nil
		pressedTabCount = 0
		ignoreNextChar = false
	}

	moveCursorLineStart := func() {
		termenv.CursorBack(len(input) + promptLen)
	}

	//prints the input with colorizations, after this function has been executed the cursor is at the end of line
	printPromptAndInput := func() {
		promptLen = writePrompt(state, config)
		var colorizations []ColorizationInfo

		mod, _ := gopherscript.ParseModule(string(input), "")

		gopherscript.Walk(mod, func(node, parent, scopeNode gopherscript.Node, ancestorChain []gopherscript.Node) (error, gopherscript.TraversalAction) {
			switch n := node.(type) {
			//literals
			case *gopherscript.IdentifierLiteral, *gopherscript.Variable, *gopherscript.GlobalVariable, *gopherscript.AtHostLiteral:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSIBrightCyan,
				})

			case *gopherscript.PatternIdentifierLiteral:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSIBrightGreen,
				})
			case *gopherscript.StringLiteral, *gopherscript.FlagLiteral:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSI256Color(209),
				})
			case *gopherscript.AbsolutePathPatternLiteral, *gopherscript.RelativePathPatternLiteral, *gopherscript.URLPatternLiteral, *gopherscript.HTTPHostPatternLiteral:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSIRed,
				})
			case *gopherscript.URLLiteral, *gopherscript.HTTPHostLiteral, *gopherscript.AbsolutePathLiteral, *gopherscript.RelativePathLiteral,
				*gopherscript.PathSlice, *gopherscript.URLQueryParameterSlice:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSI256Color(209),
				})
			case *gopherscript.IntLiteral, *gopherscript.FloatLiteral:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSIBrightGreen,
				})
			case *gopherscript.BooleanLiteral, *gopherscript.NilLiteral:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSIBlue,
				})
			case *gopherscript.InvalidURLPattern, *gopherscript.InvalidURL, *gopherscript.InvalidAliasRelatedNode, *gopherscript.InvalidComplexPatternElement,
				*gopherscript.InvalidPathSlice, *gopherscript.UnknownNode:
				colorizations = append(colorizations, ColorizationInfo{
					span:  n.Base().Span,
					color: termenv.ANSIBrightRed,
				})
				// other nodes
			case *gopherscript.IfStatement, *gopherscript.SwitchStatement, *gopherscript.MatchStatement,
				*gopherscript.ReturnStatement, *gopherscript.BreakStatement, *gopherscript.ContinueStatement, *gopherscript.SpawnExpression:
				for _, tok := range n.Base().ValuelessTokens {
					colorizations = append(colorizations, ColorizationInfo{
						span:  tok.Span,
						color: CONTROL_KEYWORD_COLOR,
					})
				}
			case *gopherscript.MultiAssignment, *gopherscript.GlobalConstantDeclarations, *gopherscript.ImportStatement,
				*gopherscript.PermissionDroppingStatement:
				for _, tok := range n.Base().ValuelessTokens {
					colorizations = append(colorizations, ColorizationInfo{
						span:  tok.Span,
						color: OTHER_KEYWORD_COLOR,
					})
				}
			case *gopherscript.OptionExpression:
				if n.Value == nil {
					break
				}

				if _, isMissingExpr := n.Value.(*gopherscript.MissingExpression); isMissingExpr {
					break
				}

				colorizations = append(colorizations, ColorizationInfo{
					span:  gopherscript.NodeSpan{Start: n.Span.Start, End: n.Value.Base().Span.Start - 1},
					color: termenv.ANSI256Color(209),
				})
			}

			return nil, gopherscript.Continue
		})

		sort.Slice(colorizations, func(i, j int) bool {
			return colorizations[i].span.Start < colorizations[j].span.Start
		})

		prev := int(0)

		for _, colorization := range colorizations {
			before := termenv.String(string(input[prev:colorization.span.Start]))

			before = before.Foreground(config.defaultFgColor)

			fmt.Print(before.String())

			s := termenv.String(string(input[colorization.span.Start:min(len(input), colorization.span.End)]))
			s = s.Foreground(colorization.color)
			fmt.Print(s.String())

			prev = colorization.span.End
		}

		if prev < len(input) {
			fmt.Print(string(input[prev:]))
		}
	}

	for {
		r, _, err := reader.ReadRune()

		if ignoreNextChar {
			ignoreNextChar = false
			continue
		}

		if err == io.EOF {
			log.Println("EOF")
		} else if err != nil {
			log.Panicln(err)
		}

		runeSeq = append(runeSeq, r)
		code := getSpecialCode(runeSeq)
		//fmt.Print(code.String())

		var left []rune
		var right []rune

		switch code {
		case Escape, EscapeNext:
			continue
		default:
			runeSeq = nil
		}

		if len(input) != 0 && code == NotSpecialCode {
			idx := len(input) - backspaceCount

			_left := input[0:idx]
			left = make([]rune, len(_left))
			copy(left, _left)

			_right := input[idx:]
			right = make([]rune, len(_right))
			copy(right, _right)
		}

		switch code {
		case ArrowUp:
			fallthrough
		case ArrowDown:
			termenv.ClearLine()
			termenv.CursorBack(len(input) + promptLen)
			reset()
			input = []rune(history.Commands[commandIndex])

			if code == ArrowUp {
				commandIndex--
			} else {
				commandIndex++
			}

			if commandIndex < 0 {
				commandIndex = len(history.Commands) - 1
			} else if commandIndex >= len(history.Commands) {
				commandIndex = 0
			}

			termenv.ClearLine()
			termenv.CursorBack(len(input) + promptLen)

			printPromptAndInput()
			continue
		case Escape:
			continue
		case Backspace:

			if len(input) == 0 || backspaceCount >= len(input) {
				continue
			}

			start := len(input) - backspaceCount - 1
			right := copyRuneSlice(input[start+1:])
			input = append(input[0:start], right...)

			termenv.CursorBack(1)
			termenv.SaveCursorPosition()

			fmt.Print(string(right))
			termenv.ClearLineRight()
			termenv.RestoreCursorPosition()

			continue
		case Home:
			prevBackspaceCount := backspaceCount
			backspaceCount = len(input)
			termenv.CursorBack(backspaceCount - prevBackspaceCount)
			continue
		case End:
			if backspaceCount == 0 {
				ignoreNextChar = true
				continue
			}
			prevBackspaceCount := backspaceCount
			backspaceCount = 0
			termenv.CursorForward(prevBackspaceCount)
			ignoreNextChar = true
			continue
		case ArrowLeft:
			if backspaceCount < len(input) {
				backspaceCount += 1
				termenv.CursorBack(1)
			}
			continue
		case ArrowRight:
			if backspaceCount > 0 {
				backspaceCount -= 1
				termenv.CursorForward(1)
			}
			continue
		case CtrlC:
			os.Exit(0)
		case Tab:
			pressedTabCount++

			if pressedTabCount == 1 {
				continue
			} else {
				pressedTabCount = 0
			}

			if len(input) == 0 {
				globals := state.GlobalScope()
				names := make([]string, 0, len(globals))
				for name, _ := range globals {
					names = append(names, name)
				}

				termenv.SaveCursorPosition()

				sort.Slice(names, func(i, j int) bool {
					return names[i][0] < names[j][0]
				})

				//print suggestions
				moveCursorLineStart()
				fmt.Printf("\n\r%s\n\r", strings.Join(names, " "))

				termenv.RestoreCursorPosition()
				break
			}

			mod, _ := gopherscript.ParseModule(string(input), "")

			cursorIndex := len(input) - backspaceCount
			suggestions := findSuggestions(state, ctx, mod, cursorIndex)

			switch len(suggestions) {
			case 0:
				//do nothing
			case 1:
				//replace the incomplete element with the suggestion
				suggestion := suggestions[0]
				beforeElem := input[:suggestion.span.Start]
				afterElem := input[suggestion.span.End:]

				prevLen := len(input)
				input = append(beforeElem, []rune(suggestion.value)...)
				input = append(input, afterElem...)
				newCharCount := len(input) - prevLen

				termenv.SaveCursorPosition()

				termenv.ClearLine()
				moveCursorLineStart()
				printPromptAndInput()

				termenv.RestoreCursorPosition()
				termenv.CursorForward(newCharCount)
			default:
				var texts []string
				for _, sug := range suggestions {
					texts = append(texts, sug.shownString)
				}
				termenv.SaveCursorPosition()

				sort.Slice(texts, func(i, j int) bool {
					return texts[i][0] < texts[j][0]
				})

				//print suggestions
				moveCursorLineStart()
				fmt.Printf("\n\r%s\n\r", strings.Join(texts, " "))

				termenv.RestoreCursorPosition()
			}

		case Enter:
			if strings.Trim(string(input), " ") != "" {
				history.Commands = append(history.Commands, string(input))
				if history.Commands[0] == "" {
					history.Commands = history.Commands[1:]
				} else {
					commandIndex++
				}
			}

			inputString := string(input)

			fmt.Print("\n\r")
			termenv.ClearLine()
			termenv.CursorNextLine(1)

			splitted := strings.Split(inputString, " ")

			switch splitted[0] {
			case "clear":
				reset()
				termenv.ClearScreen()

			default:
				reset()

				mod, err := gopherscript.ParseAndCheckModule(inputString, "")
				if err != nil {
					errString := replaceNewLinesRawMode(err.Error())
					fmt.Print(errString, "\n\r")
				} else {
					res, evalErr := gopherscript.Eval(mod, state)
					if evalErr != nil {
						errString := replaceNewLinesRawMode(evalErr.Error())
						fmt.Print(errString, "\n\r")
					} else {
						if list, ok := res.(gopherscript.List); !ok || len(list) != 0 {
							fmt.Printf("%#v\n\r", res)
						}
					}
				}

				termenv.CursorNextLine(1)
			}

			promptLen = writePrompt(state, config)
			continue
		}

		if strconv.IsPrint(r) {
			termenv.ClearLine()
			moveCursorLineStart()
			input = append(left, r)

			//we append the corresponding closing delimiter if the new rune is an opening delimiter
			switch r {
			case '[', '{', '(':
				input = append(input, getClosingDelimiter(r))
				backspaceCount++
			}

			input = append(input, right...)
			printPromptAndInput()

			if backspaceCount > 0 {
				termenv.CursorBack(backspaceCount)
			}
		} else {
			//fmt.Printf("%v", r)
		}
	}
}

func moveFlagsStart(args []string) {
	index := 0

	for i := range args {
		if args[i] == "--" {
			break
		}
		if args[i][0] == '-' {
			temp := args[i]
			args[i] = args[index]
			args[index] = temp
			index++
		}
	}
}

func findLongestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}

	if len(strs) == 1 {
		return strs[0]
	}

	var runeSlices [][]rune
	for _, s := range strs {
		runeSlices = append(runeSlices, []rune(s))
	}

	var prefix []rune
	for i := 0; i < len(runeSlices[0]); i++ {
		for j := 1; j < len(runeSlices); j++ {
			if runeSlices[j][i] == runeSlices[0][i] {
				continue
			} else {
				return string(prefix)
			}
		}
		prefix = append(prefix, runeSlices[0][i])
	}

	return string(prefix)
}

type suggestion struct {
	shownString string
	value       string
	span        gopherscript.NodeSpan
}

func findPathSuggestions(ctx *gopherscript.Context, pth string) []suggestion {

	var suggestions []suggestion

	dir := path.Dir(pth)
	base := path.Base(pth)

	if gopherscript.Path(pth).IsDirPath() {
		base = ""
	}

	entries, err := fsLs(ctx, gopherscript.Path(dir+"/"))
	if err != nil {
		return nil
	}

	for _, e := range entries {
		name := e.(gopherscript.Object)["name"].(string)
		if strings.HasPrefix(name, base) {
			pth := path.Join(dir, name)

			if !gopherscript.HasPathLikeStart(pth) {
				pth = "./" + pth
			}

			stat, _ := os.Stat(pth)
			if stat.IsDir() {
				pth += "/"
			}

			suggestions = append(suggestions, suggestion{
				shownString: name,
				value:       pth,
			})
		}
	}

	return suggestions
}

func findSuggestions(state *gopherscript.State, ctx *gopherscript.Context, mod *gopherscript.Module, cursorIndex int) []suggestion {

	var suggestions []suggestion

	var nodeAtCursor gopherscript.Node

	gopherscript.Walk(mod, func(node, parent, scopeNode gopherscript.Node, ancestorChain []gopherscript.Node) (error, gopherscript.TraversalAction) {
		span := node.Base().Span

		if span.Start > cursorIndex || span.End < cursorIndex {
			return nil, gopherscript.Prune
		}

		if nodeAtCursor == nil || node.Base().IncludedIn(nodeAtCursor) {
			nodeAtCursor = node

			if _, isIdentMemberExpr := parent.(*gopherscript.IdentifierMemberExpression); isIdentMemberExpr {
				nodeAtCursor = parent
			}

		}

		return nil, gopherscript.Continue
	})

	if nodeAtCursor == nil {
		return nil
	}

switch_:
	switch n := nodeAtCursor.(type) {
	case *gopherscript.IdentifierLiteral:
		for name, _ := range state.GlobalScope() {
			if strings.HasPrefix(name, n.Name) {
				suggestions = append(suggestions, suggestion{
					shownString: name,
					value:       name,
				})
			}
		}
	case *gopherscript.IdentifierMemberExpression:
		val, ok := state.GlobalScope()[n.Left.Name]
		if !ok {
			break
		}

		curr := val

		buff := bytes.NewBufferString(n.Left.Name)

		for i, propName := range n.PropertyNames {
			next, _, _ := gopherscript.Memb(curr, propName.Name)

			if next == nil { // if the member does not exist

				if obj, ok := curr.(gopherscript.Object); ok && i == len(n.PropertyNames)-1 {

					s := buff.String()

					for actualPropName, _ := range obj {

						if !strings.HasPrefix(actualPropName, propName.Name) {
							continue
						}

						suggestions = append(suggestions, suggestion{
							shownString: s + "." + actualPropName,
							value:       s + "." + actualPropName,
						})
					}
					break switch_
				}
			} else { //if the member exist
				buff.WriteRune('.')
				buff.WriteString(propName.Name)
				curr = next
			}
		}

		s := buff.String()

		if n.Err != nil && strings.Contains(n.Err.Message, "unterminated") {

			if obj, ok := curr.(gopherscript.Object); ok {

				for actualPropName, _ := range obj {
					suggestions = append(suggestions, suggestion{
						shownString: s + "." + actualPropName,
						value:       s + "." + actualPropName,
					})
				}
			}
		}

		suggestions = append(suggestions, suggestion{
			shownString: s,
			value:       s,
		})

	case *gopherscript.RelativePathLiteral:
		suggestions = findPathSuggestions(ctx, n.Value)
	case *gopherscript.AbsolutePathLiteral:
		suggestions = findPathSuggestions(ctx, n.Value)

	}

	for i, suggestion := range suggestions {
		suggestion.span = nodeAtCursor.Base().Span
		suggestions[i] = suggestion
	}

	return suggestions
}

type REPLConfiguration struct {
	builtinCommands []string
	aliasedCommands []string
	prompt          gopherscript.List
	defaultFgColor  termenv.Color
}

func makeConfiguration(obj gopherscript.Object) (REPLConfiguration, error) {
	var config REPLConfiguration

	config.defaultFgColor = termenv.ForegroundColor()

	for k, v := range obj {
		switch k {
		case "builtin-commands":
			BUILTIN_COMMAND_LIST_ERR := "invalid configuration: builtin-commands should be a list of identifiers"
			list, isList := v.(gopherscript.List)
			if !isList {
				return config, errors.New(BUILTIN_COMMAND_LIST_ERR)
			}
			for _, cmd := range list {
				ident, ok := cmd.(gopherscript.Identifier)
				if !ok {
					return config, errors.New(BUILTIN_COMMAND_LIST_ERR)
				}
				config.builtinCommands = append(config.builtinCommands, string(ident))
			}
		case "aliased-commands":
			ALIASED_COMMAND_LIST_ERR := "invalid configuration: aliased-commands should be a list of identifiers"
			list, isList := v.(gopherscript.List)
			if !isList {
				return config, errors.New(ALIASED_COMMAND_LIST_ERR)
			}
			for _, cmd := range list {
				ident, ok := cmd.(gopherscript.Identifier)
				if !ok {
					return config, errors.New(ALIASED_COMMAND_LIST_ERR)
				}
				config.aliasedCommands = append(config.aliasedCommands, string(ident))
			}
		case "prompt":
			PROMPT_CONFIG_ERR := "invalid configuration: prompt should be a list"
			list, isList := v.(gopherscript.List)
			if !isList {
				return config, errors.New(PROMPT_CONFIG_ERR)
			}
			for _, part := range list {

				if list, isList := part.(gopherscript.List); isList {
					if len(list) != 2 {
						return config, fmt.Errorf("invalid configuration: parts of type List should be two element long: [<desc.>, <color identifier>]")
					}
					part = list[0]
				}

				switch p := part.(type) {
				case string:
				// case gopherscript.Identifier:
				// 	switch part {
				// 	case "cwd":
				// 	default:
				// 		return config, fmt.Errorf("invalid configuration: invalid part in prompt configuration: %s is not valid identifier", p)
				// 	}
				case *gopherscript.LazyExpression:
				default:
					return config, fmt.Errorf("invalid configuration: invalid part in prompt configuration: type %T", p)
				}
			}
			config.prompt = list
		}
	}

	return config, nil
}

func main() {

	switch len(os.Args) {
	case 1:
		log.Panicln("missing subcommand")
	default:
		switch os.Args[1] {
		case "run":
			if len(os.Args) == 2 {
				panic("missing script path")
			}

			runFlags := flag.NewFlagSet("run", flag.ExitOnError)
			var perms string
			runFlags.StringVar(&perms, "p", "", "granted permissions to the script")

			subcommandArgs := os.Args[2:]
			moveFlagsStart(subcommandArgs)

			err := runFlags.Parse(subcommandArgs)
			if err != nil {
				panic(err)
			}

			filepath := runFlags.Arg(0)
			var passedArgs []string
			if len(runFlags.Args()) > 2 {
				passedArgs = runFlags.Args()[2:]
			}

			if filepath == "" {
				panic("missing script path")
			}

			if info, err := os.Stat(filepath); err == os.ErrNotExist || (err == nil && info.IsDir()) {
				panic(fmt.Sprint(filepath, "does not exist or is a folder"))
			}
			b, err := os.ReadFile(filepath)
			if err != nil {
				panic(fmt.Sprint("failed to read", filepath, err.Error()))
			}
			code := string(b)
			mod, err := gopherscript.ParseModule(code, filepath)
			if err != nil {
				panic(fmt.Sprint("parsing error: ", err.Error()))
			}

			if err := gopherscript.Check(mod); err != nil {
				panic(fmt.Sprint("checking error: ", err.Error()))
			}

			var ctx *gopherscript.Context
			passCLIArguments := false
			if mod.Requirements == nil {
				panic("missing requirements in script")
			}

			requiredPermissions, limitations := mod.Requirements.Object.PermissionsLimitations(
				mod.GlobalConstantDeclarations,
				nil,
				func(kind gopherscript.PermissionKind, name string, value gopherscript.Node) ([]gopherscript.Permission, bool, error) {
					if kind != gopherscript.ReadPerm || name != "cli-args" {
						return nil, false, nil
					}
					boolLit, ok := value.(*gopherscript.BooleanLiteral)
					if !ok {
						return nil, true, errors.New("cli-args should have a boolean value")
					}

					if boolLit.Value {
						passCLIArguments = true
					}
					return nil, true, nil //okay to not give a permission ???
				},
			)

			if perms == "required" {
				ctx = gopherscript.NewContext(requiredPermissions, nil, limitations)
			} else if len(requiredPermissions) != 0 {
				panic("some required permissions are not granted. Did you use -p=required ?")
			}

			//CONTEXT & STATE

			state := NewState(ctx)
			state.Script = []rune(code)
			state.ScriptName = filepath

			if passCLIArguments {
				args := gopherscript.List{}
				for _, arg := range passedArgs {
					args = append(args, arg)
				}
				state.GlobalScope()["args"] = args
			}

			//EXECUTION

			res, err := gopherscript.Eval(mod, state)
			if err != nil {
				fmt.Print(err, "\n")
			} else {
				if list, ok := res.(gopherscript.List); (!ok && res != nil) || len(list) != 0 {
					fmt.Printf("%#v\n\r", res)
				}
			}
		case "repl":
			replFlags := flag.NewFlagSet("repl", flag.ExitOnError)
			var startupScriptPath string

			home, err := os.UserHomeDir()
			if err == nil && home != "" {
				pth := path.Join(home, "startup.gos")
				info, err := os.Stat(startupScriptPath)
				if err == nil && info.Mode().IsRegular() {
					startupScriptPath = pth
				}
			}

			replFlags.StringVar(&startupScriptPath, "c", startupScriptPath, "startup script path")

			subcommandArgs := os.Args[2:]
			moveFlagsStart(subcommandArgs)

			err = replFlags.Parse(subcommandArgs)
			if err != nil {
				log.Panicln(err)
			}

			if startupScriptPath == "" {
				panic("no startup file found in homedir and none was specified (-c <file>). You can fix this by copying the startup.gos file from the Gopherscript repository to your home directory.")
			}

			//we read, parse and evaluate the startup script

			b, err := os.ReadFile(startupScriptPath)
			if err != nil {
				panic(fmt.Sprint("failed to read startup file ", startupScriptPath, ":", err))
			}

			startupMod, err := gopherscript.ParseAndCheckModule(string(b), "")
			if err != nil {
				log.Panicln("failed to parse & check startup file:", err)
			}
			requiredPermissions, limitations := startupMod.Requirements.Object.PermissionsLimitations(startupMod.GlobalConstantDeclarations, nil, nil)
			ctx := gopherscript.NewContext(requiredPermissions, nil, limitations)
			state := NewState(ctx)

			startupResult, err := gopherscript.Eval(startupMod, state)
			if err != nil {
				panic(fmt.Sprint("startup script failed:", err))
			}

			//REPL configuration

			var config REPLConfiguration

			switch r := startupResult.(type) {
			case gopherscript.Object:
				config, err = makeConfiguration(r)
				if strSliceContains(config.builtinCommands, "cd") {
					state.GlobalScope()["cd"] = gopherscript.ValOf(func(ctx *gopherscript.Context, newdir gopherscript.Path) error {
						if !newdir.IsDirPath() {
							return errors.New("cd: the path must be a directory path")
						}

						if err := os.Chdir(string(newdir)); err != nil {
							return errors.New("cd: " + err.Error())
						}
						return nil
					})
				}
				if err != nil {
					log.Println(replaceNewLinesRawMode(err.Error()))
				}

				//add aliased commands to the global scope

				globalScope := state.GlobalScope()
				for _, cmd := range config.aliasedCommands {
					if _, alreadyPresent := globalScope[cmd]; alreadyPresent {
						panic(errors.New("aliased commands cannot override a global variable"))
					}
					globalScope[cmd] = func(cmd string) interface{} {
						return gopherscript.ValOf(func(ctx *gopherscript.Context, args ...interface{}) (string, error) {
							return ex(ctx, gopherscript.Identifier(cmd), args...)
						})
					}(cmd)
				}
			case nil:
			default:
				panic(fmt.Sprintf("startup script should return an Object or nothing (nil), not a(n) %T", r))
			}

			//--------------------------

			startShell(state, ctx, config)
		default:
			panic(fmt.Sprint("unknown subcommand", os.Args[1]))
		}
	}
}

type CommandResult struct {
}

func ex(ctx *gopherscript.Context, cmdName gopherscript.Identifier, args ...interface{}) (string, error) {

	var subcommandNameChain []string
	var cmdArgs []string

	//we remove the subcommand chain from <argss>
	for len(args) != 0 {
		name, ok := args[0].(gopherscript.Identifier)
		if ok {
			subcommandNameChain = append(subcommandNameChain, string(name))
			args = args[1:]
		} else {
			break
		}
	}

	//we check that remaining args are simple values
	for _, arg := range args {
		if gopherscript.IsSimpleGopherVal(arg) {
			cmdArgs = append(cmdArgs, fmt.Sprint(arg))
		} else {
			return "", fmt.Errorf("ex: invalid argument %v of type %T, only simple values are allowed", arg, arg)
		}
	}

	perm := gopherscript.CommandPermission{
		CommandName:         string(cmdName),
		SubcommandNameChain: subcommandNameChain,
	}

	if err := ctx.CheckHasPermission(perm); err != nil {
		return "", err
	}

	passedArgs := make([]string, 0)
	passedArgs = append(passedArgs, subcommandNameChain...)
	passedArgs = append(passedArgs, cmdArgs...)

	b, err := exec.Command(string(cmdName), passedArgs...).Output()
	return string(b), err
}

func NewState(ctx *gopherscript.Context) *gopherscript.State {

	var state *gopherscript.State
	state = gopherscript.NewState(ctx, map[string]interface{}{
		"ex": ex,
		"fs": gopherscript.Object{
			"mkfile": gopherscript.ValOf(fsMkfile),
			"mkdir":  gopherscript.ValOf(fsMkdir),
			"read":   gopherscript.ValOf(fsReadFile),
			"ls":     gopherscript.ValOf(fsLs),
			"del":    gopherscript.ValOf(fsDel),
		},
		"io": gopherscript.Object{
			"ReadAll": gopherscript.ValOf(func(ctx *gopherscript.Context, reader io.Reader) ([]byte, error) {
				return io.ReadAll(reader)
			}),
		},
		"http": gopherscript.Object{
			"get": gopherscript.ValOf(httpGet),
			"getbody": gopherscript.ValOf(func(ctx *gopherscript.Context, args ...interface{}) ([]byte, error) {
				resp, err := httpGet(ctx, args...)
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, err
				}
				return b, nil
			}),
			"post":   gopherscript.ValOf(httpPost),
			"patch":  gopherscript.ValOf(httpPatch),
			"delete": gopherscript.ValOf(httpDelete),
			"serve": gopherscript.ValOf(func(ctx *gopherscript.Context, args ...interface{}) (*httpServer, error) {
				var addr string
				var handler http.Handler
				var sharedGlobals gopherscript.KeyList

				for _, arg := range args {
					switch v := arg.(type) {
					case gopherscript.HTTPHost:
						if addr != "" {
							return nil, errors.New("address already provided")
						}
						parsed, _ := url.Parse(string(v))
						addr = parsed.Host

						perm := gopherscript.HttpPermission{Kind_: gopherscript.ProvidePerm, Entity: v}
						if err := ctx.CheckHasPermission(perm); err != nil {
							return nil, err
						}
					case gopherscript.Func:
						if handler != nil {
							return nil, errors.New("handler already provided")
						}

						handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							newState := NewState(ctx)
							newStateGlobals := newState.GlobalScope()
							stateGlobals := state.GlobalScope()

							for _, name := range sharedGlobals {
								newStateGlobals[name] = stateGlobals[name]
							}

							req := httpRequest{
								Method:  r.Method,
								URL:     gopherscript.URL(r.URL.String()),
								Path:    gopherscript.Path(r.URL.Path),
								Body:    r.Body,
								request: r,
							}

							log.Println(replaceNewLinesRawMode(fmt.Sprintf("%#v", req)))

							resp := &httpResponse{
								rw: w,
							}

							_, err := gopherscript.CallFunc(v, newState, gopherscript.List{
								gopherscript.ValOf(resp),
								gopherscript.ValOf(req),
							}, false)

							if err != nil {
								log.Println(err)
								termenv.CursorNextLine(1)
							}
						})
					case gopherscript.KeyList:
						if sharedGlobals != nil {
							return nil, errors.New("list of shared globals already provided")
						}
						sharedGlobals = v
					default:
						return nil, fmt.Errorf("http.serve: invalid argument of type %T", v)
					}
				}

				if addr == "" {
					return nil, errors.New("no address required")
				}

				//if no handler was provided we set a default handler that writes "hello"
				if handler == nil {
					mux := http.NewServeMux()
					mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
						w.Write([]byte("hello"))
					})
					handler = mux
				}

				server, certFile, keyFile, err := makeHttpServer(addr, handler, "", "")
				if err != nil {
					return nil, err
				}

				endChan := make(chan (interface{}))

				go func() {
					log.Println(server.ListenAndServeTLS(certFile, keyFile))
					endChan <- 0
				}()

				time.Sleep(5 * time.Millisecond)
				log.Println("serve", addr)

				return &httpServer{
					endChan: endChan,
				}, nil
			}),
			"servedir": gopherscript.ValOf(gopherscript.ValOf(func(ctx *gopherscript.Context, args ...interface{}) (*httpServer, error) {
				var addr string
				var dir gopherscript.Path

				for _, arg := range args {
					switch v := arg.(type) {
					case gopherscript.HTTPHost:
						if addr != "" {
							return nil, errors.New("address already provided")
						}
						parsed, _ := url.Parse(string(v))
						addr = parsed.Host

						perm := gopherscript.HttpPermission{Kind_: gopherscript.ProvidePerm, Entity: v}
						if err := ctx.CheckHasPermission(perm); err != nil {
							return nil, err
						}
					case gopherscript.Path:
						if !v.IsDirPath() {
							return nil, errors.New("the directory path should end with '/'")
						}
						dir = v
					default:
					}
				}

				if addr == "" {
					return nil, errors.New("no address provided")
				}

				if dir == "" {
					return nil, errors.New("no (directory) path required")
				}

				server, certFile, keyFile, err := makeHttpServer(addr, http.FileServer(http.Dir(dir)), "", "")
				if err != nil {
					return nil, err
				}

				endChan := make(chan (interface{}))

				go func() {
					log.Println(server.ListenAndServeTLS(certFile, keyFile))
					endChan <- 0
				}()

				time.Sleep(5 * time.Millisecond)
				return &httpServer{
					endChan: endChan,
				}, nil
			})),
			"servefile": gopherscript.ValOf(func(ctx *gopherscript.Context, rw *httpResponse, r httpRequest, pth gopherscript.Path) error {

				pth = pth.ToAbs()
				perm := gopherscript.FilesystemPermission{Kind_: gopherscript.ReadPerm, Entity: pth}

				if err := ctx.CheckHasPermission(perm); err != nil {
					return err
				}

				http.ServeFile(rw.rw, r.request, string(pth))
				return nil
			}),
		},
		"logvals": func(ctx *gopherscript.Context, args ...interface{}) {
			s := ""
			for _, arg := range args {
				s += fmt.Sprintf("%#v", arg)
			}
			log.Println(s)
			termenv.CursorNextLine(1)
		},
		"log": func(ctx *gopherscript.Context, args ...interface{}) {
			log.Println(args...)
			termenv.CursorNextLine(1)
		},
		"println":  fmt.Println,
		"nilerror": (*error)(nil),
		"dummyerr": errors.New("dummy error"),
		"append": func(ctx *gopherscript.Context, slice gopherscript.List, args ...interface{}) gopherscript.List {
			return append(slice, args...)
		},
		"tostr": func(ctx *gopherscript.Context, arg interface{}) string {
			return fmt.Sprintf("%s", arg)
		},
		"ago": func(ctx *gopherscript.Context, d time.Duration) time.Time {
			//return error if d negative ?
			return time.Now().Add(-d)
		},
		"map": func(ctx *gopherscript.Context, filter interface{}, list gopherscript.List) (gopherscript.List, error) {
			result := gopherscript.List{}

			switch fil := filter.(type) {
			case gopherscript.Node:

				//should ctx allow to do that instead ?
				state.PushScope()
				defer state.PopScope()

				for _, e := range list {
					state.CurrentScope()[""] = e
					res, err := gopherscript.Eval(fil.(*gopherscript.LazyExpression).Expression, state)
					if err != nil {
						return nil, err
					}
					result = append(result, res)
				}
			case gopherscript.KeyList:
				for _, e := range list {
					res := gopherscript.Object{}

					for _, name := range fil {
						res[name] = e.(gopherscript.Object)[name]
					}

					result = append(result, res)
				}
			}

			return result, nil
		},
		"filter": func(ctx *gopherscript.Context, filter interface{}, list gopherscript.List) (gopherscript.List, error) {
			result := gopherscript.List{}

			switch fil := filter.(type) {
			case gopherscript.Node:
				state.PushScope()
				defer state.PopScope()

				for _, e := range list {
					state.CurrentScope()[""] = e
					res, err := gopherscript.Eval(fil.(*gopherscript.LazyExpression).Expression, state)
					if err != nil {
						return nil, err
					}
					if res.(bool) {
						result = append(result, e)
					}
				}
			case gopherscript.Matcher:
				for _, e := range list {
					if fil.Test(e) {
						result = append(result, e)
					}
				}
			default:
				return nil, fmt.Errorf("invalid filter : type is %T", fil)
			}

			return result, nil
		},
		"some": func(ctx *gopherscript.Context, node gopherscript.Node, list gopherscript.List) (bool, error) {
			state.PushScope()
			defer state.PopScope()

			ok := false

			for _, e := range list {
				state.CurrentScope()[""] = e
				res, err := gopherscript.Eval(node.(*gopherscript.LazyExpression).Expression, state)
				if err != nil {
					return false, err
				}
				if res.(bool) {
					ok = true
				}
			}

			return ok, nil
		},
		"mkbytes": func(ctx *gopherscript.Context, size int) ([]byte, error) {
			return make([]byte, size), nil
		},
		"tojson":    toJSON,
		"topjson":   toPrettyJSON,
		"tojsonval": toJSONVal,
		"toval":     toGopherscriptVal,
		"parsejson": parseJson,
		"sleep": func(ctx *gopherscript.Context, d time.Duration) {
			time.Sleep(d)
		},

		"mime": mime_,
		"read": func(ctx *gopherscript.Context, args ...interface{}) (interface{}, error) {
			var resource interface{}
			var contentType mimetype

			for _, arg := range args {
				switch v := arg.(type) {
				case gopherscript.URL, gopherscript.Path:
					if resource != nil {
						return nil, errors.New("resource provided at least twice")
					}
					resource = arg
				case mimetype:
					if contentType != "" {
						return nil, errors.New("content type provided at least twice")
					}
					contentType = v
				default:
					return nil, fmt.Errorf("invalid argument %#v", arg)
				}
			}

			switch res := resource.(type) {
			case gopherscript.URL:
				resp, err := httpGet(ctx, res)

				if resp != nil {
					defer resp.Body.Close()
				}
				if err != nil {
					return nil, fmt.Errorf("read: http: %s", err.Error())
				} else {
					b, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, fmt.Errorf("read: http: body: %s", err.Error())
					}

					respContentType, err := mime_(ctx, resp.Header.Get("Content-Type"))

					if err == nil && contentType == "" {
						contentType = respContentType
					}

					switch contentType.WithoutParams() {
					case JSON_CTYPE, HTML_CTYPE, PLAIN_TEXT_CTYPE:
						return string(b), nil
					}

					return b, nil
				}
			case gopherscript.Path:
				if res.IsDirPath() {
					return fsLs(ctx, res)
				} else {
					b, err := __readEntireFile(ctx, res)
					if err != nil {
						return nil, err
					}
					switch contentType {
					case JSON_CTYPE, HTML_CTYPE, PLAIN_TEXT_CTYPE:
						return string(b), nil
					}
					return b, nil
				}
			default:
				return nil, fmt.Errorf("resources of type %T not supported yet", res)
			}
		},
		"create": func(ctx *gopherscript.Context, args ...interface{}) (interface{}, error) {
			var resource interface{}
			var content io.Reader

			for _, arg := range args {
				switch v := arg.(type) {
				case gopherscript.URL, gopherscript.Path:
					if resource != nil {
						return nil, errors.New("resource provided at least twice")
					}
					resource = arg
				case string:
					if content != nil {
						return nil, errors.New("content provided at least twice")
					}
					content = strings.NewReader(v)
				case []byte:
					if content != nil {
						return nil, errors.New("content provided at least twice")
					}
					content = bytes.NewReader(v)
				default:
					return nil, fmt.Errorf("invalid argument %#v", arg)
				}
			}

			if resource == nil {
				return nil, errors.New("missing resource")
			}

			switch res := resource.(type) {
			case gopherscript.URL:
				resp, err := httpPost(ctx, res, content)
				if resp != nil {
					defer resp.Body.Close()
				}

				if err != nil {
					io.ReadAll(resp.Body)
					return nil, fmt.Errorf("create: http: %s", err.Error())
				} else {
					contentType := resp.Header.Get("Content-Type")
					b, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, fmt.Errorf("create: http: body: %s", err.Error())
					}

					switch contentType {
					case JSON_CTYPE, HTML_CTYPE, PLAIN_TEXT_CTYPE:
						return string(b), nil
					}
					return b, nil
				}
			case gopherscript.Path:
				if res.IsDirPath() {
					return nil, fsMkdir(ctx, res)
				} else {
					return nil, fsMkfile(ctx, args...)
				}
			default:
				return nil, fmt.Errorf("resources of type %T not supported yet", res)
			}
		},
		"update": func(ctx *gopherscript.Context, args ...interface{}) (interface{}, error) {
			var resource interface{}
			var content io.Reader
			var mode gopherscript.Identifier

			for _, arg := range args {
				switch v := arg.(type) {
				case gopherscript.URL, gopherscript.Path:
					if resource != nil {
						return nil, errors.New("resource provided at least twice")
					}
					resource = arg
				case string:
					if content != nil {
						return nil, errors.New("content provided at least twice")
					}
					content = strings.NewReader(v)
				case gopherscript.Identifier:
					if mode != "" {
						return nil, errors.New("mode provided at least twice")
					}

					switch v {
					case "append":
						mode = v
					default:
						return nil, fmt.Errorf("invalid mode '%s'", v)
					}
				default:
					return nil, fmt.Errorf("invalid argument e %#v", arg)
				}
			}

			if resource == nil {
				return nil, errors.New("missing resource")
			}

			switch res := resource.(type) {
			case gopherscript.URL:

				if mode != "" {
					return nil, errors.New("update: http does not support append mode yet")
				}

				resp, err := httpPatch(ctx, res, content)
				if resp != nil {
					defer resp.Body.Close()
				}

				if err != nil {
					return nil, fmt.Errorf("update: http: %s", err.Error())
				} else {
					contentType := resp.Header.Get("Content-Type")
					b, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, fmt.Errorf("update: http: body: %s", err.Error())
					}

					switch contentType {
					case JSON_CTYPE, HTML_CTYPE, PLAIN_TEXT_CTYPE:
						return string(b), nil
					}
					return b, nil
				}
			case gopherscript.Path:
				if res.IsDirPath() {
					return nil, errors.New("update: directories not supported")
				} else {
					return nil, fsAppendToFile(ctx, resource, content)
				}
			default:
				return nil, fmt.Errorf("resources of type %T not supported yet", res)
			}
		},
		"delete": func(ctx *gopherscript.Context, args ...interface{}) (interface{}, error) {
			var resource interface{}

			for _, arg := range args {
				switch arg.(type) {
				case gopherscript.URL, gopherscript.Path:
					if resource != nil {
						return nil, errors.New("resource provided at least twice")
					}
					resource = arg
				default:
					return nil, fmt.Errorf("invalid argument %#v", arg)
				}
			}

			if resource == nil {
				return nil, errors.New("missing resource")
			}

			switch res := resource.(type) {
			case gopherscript.URL:
				resp, err := httpDelete(ctx, res)
				if resp != nil {
					defer resp.Body.Close()
				}

				if err != nil {
					return nil, fmt.Errorf("delete: http: %s", err.Error())
				} else {
					contentType := resp.Header.Get("Content-Type")
					b, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, fmt.Errorf("delete: http: body: %s", err.Error())
					}

					switch contentType {
					case JSON_CTYPE, HTML_CTYPE, PLAIN_TEXT_CTYPE:
						return string(b), nil
					}
					return b, nil
				}
			case gopherscript.Path:
				return nil, fsDel(ctx, res)
			default:
				return nil, fmt.Errorf("resources of type %T not supported yet", res)
			}
		},
		"open-store": func(ctx *gopherscript.Context, fpath gopherscript.Path) (*SmallKVStore, error) {
			return OpenOrCreateStore(ctx, fpath)
		},
		"rand": _rand,
	})

	state.GlobalScope()["tui"] = newTuiNamespace(state)
	return state
}

func fsLs(ctx *gopherscript.Context, args ...interface{}) (gopherscript.List, error) {
	var pth gopherscript.Path
	var patt gopherscript.PathPattern
	ERR := "a single path (or path pattern) argument is expected"

	for _, arg := range args {
		switch v := arg.(type) {
		case gopherscript.Path:
			if pth != "" {
				return nil, errors.New(ERR)
			}
			pth = v
		case gopherscript.PathPattern:
			if patt != "" {
				return nil, errors.New(ERR)
			}
			patt = v
		default:
			return nil, errors.New("invalid argument " + fmt.Sprintf("%#v", v))
		}
	}

	if pth == "" && patt == "" {
		pth = "./"
	}

	if pth != "" {
		pth = pth.ToAbs()
		if !pth.IsDirPath() {
			return nil, errors.New("only directory paths are supported : " + string(pth))
		}
	}

	if pth != "" && patt != "" {
		return nil, errors.New(ERR)
	}

	resultFileInfo := make(gopherscript.List, 0)

	makeEntry := func(info fs.FileInfo, pth string) gopherscript.Object {
		if info.IsDir() && pth[len(pth)-1] != '/' {
			pth += "/"
		}
		return gopherscript.Object{
			"name":    info.Name(),
			"absPath": gopherscript.Path(pth).ToAbs(),
			"size":    info.Size(),
			"mode":    info.Mode(),
			"modTime": info.ModTime(),
			"isDir":   info.IsDir(),
		}
	}

	if pth != "" {

		perm := gopherscript.FilesystemPermission{
			Kind_:  gopherscript.ReadPerm,
			Entity: pth,
		}

		if err := ctx.CheckHasPermission(perm); err != nil {
			return nil, err
		}

		entries, err := os.ReadDir(string(pth))

		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			fpath := path.Join(string(pth), entry.Name())
			info, err := os.Stat(fpath)
			if err != nil {
				return nil, err
			}

			resultFileInfo = append(resultFileInfo, makeEntry(info, fpath))

		}
	} else { //pattern
		perm := gopherscript.FilesystemPermission{
			Kind_:  gopherscript.ReadPerm,
			Entity: patt.ToAbs(),
		}

		if err := ctx.CheckHasPermission(perm); err != nil {
			return nil, err
		}

		matches, err := filepath.Glob(string(patt))

		if err != nil {
			return nil, err
		}

		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				return nil, err
			}

			resultFileInfo = append(resultFileInfo, makeEntry(info, match))
		}
	}

	return resultFileInfo, nil

}

type mimetype string

func mime_(ctx *gopherscript.Context, arg string) (mimetype, error) {
	switch arg {
	case "json":
		arg = JSON_CTYPE
	case "text":
		arg = PLAIN_TEXT_CTYPE
	}

	_, _, err := mime.ParseMediaType(arg)
	if err != nil {
		return "", fmt.Errorf("'%s' is not a MIME type: %s", arg, err.Error())
	}

	return mimetype(arg), nil
}

func (mt mimetype) WithoutParams() string {
	return strings.Split(string(mt), ";")[0]
}

func fsMkdir(ctx *gopherscript.Context, arg interface{}) error {
	var dirpath gopherscript.Path

	switch v := arg.(type) {
	case gopherscript.Path:
		if dirpath != "" {
			return errors.New(PATH_ARG_PROVIDED_TWICE)
		}
		dirpath = v
		if !dirpath.IsDirPath() {
			return errors.New("path argument is a file path : " + string(dirpath))
		}
	default:
		return errors.New("invalid argument " + fmt.Sprintf("%#v", v))
	}

	if dirpath == "" {
		return errors.New("missing path argument")
	}

	perm := gopherscript.FilesystemPermission{Kind_: gopherscript.CreatePerm, Entity: dirpath}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return err
	}

	return os.Mkdir(string(dirpath), DEFAULT_DIR_FMODE)
}

func fsMkfile(ctx *gopherscript.Context, args ...interface{}) error {
	var fpath gopherscript.Path
	var b []byte

	for _, arg := range args {
		switch v := arg.(type) {
		case gopherscript.Path:
			if fpath != "" {
				return errors.New(PATH_ARG_PROVIDED_TWICE)
			}
			fpath = v
		case string:
			if b != nil {
				return errors.New(CONTENT_ARG_PROVIDED_TWICE)
			}
			b = []byte(v)
		case []byte:
			b = v
		default:
			return errors.New("invalid argument " + fmt.Sprintf("%#v", v))
		}
	}

	if fpath == "" {
		return errors.New("missing path argument")
	}

	return __createFile(ctx, fpath, []byte(b), DEFAULT_FILE_FMODE)
}

func fsReadFile(ctx *gopherscript.Context, args ...interface{}) (interface{}, error) {
	var fpath gopherscript.Path

	for _, arg := range args {
		switch v := arg.(type) {
		case gopherscript.Path:
			if fpath != "" {
				return nil, errors.New(PATH_ARG_PROVIDED_TWICE)
			}
			fpath = v
		default:
			return nil, errors.New("invalid argument " + fmt.Sprintf("%#v", v))
		}
	}

	if fpath == "" {
		return nil, errors.New("missing path argument")
	}

	return __readEntireFile(ctx, fpath)
}

func fsAppendToFile(ctx *gopherscript.Context, args ...interface{}) error {
	var fpath gopherscript.Path
	var content io.Reader

	for _, arg := range args {
		switch v := arg.(type) {
		case gopherscript.Path:
			if fpath != "" {
				return errors.New(PATH_ARG_PROVIDED_TWICE)
			}
			fpath = v
		case string:
			if content != nil {
				return errors.New(CONTENT_ARG_PROVIDED_TWICE)
			}
			content = strings.NewReader(v)
		case io.Reader:
			if content != nil {
				return errors.New(CONTENT_ARG_PROVIDED_TWICE)
			}
			content = v
		default:
			return errors.New("invalid argument " + fmt.Sprintf("%#v", v))
		}
	}

	if fpath == "" {
		return errors.New("missing path argument")
	}

	fpath = fpath.ToAbs()

	perm := gopherscript.FilesystemPermission{Kind_: gopherscript.UpdatePerm, Entity: fpath}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return err
	}

	_, err := os.Stat(string(fpath))
	if os.IsNotExist(err) {
		return fmt.Errorf("cannot append to file: %s does not exist", fpath)
	}

	b, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("cannot append to file: %s", err.Error())
	}

	f, err := os.OpenFile(string(fpath), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to append to file: failed to open file: %s", err.Error())
	}

	defer f.Close()

	_, err = f.Write(b)
	if err != nil {
		return fmt.Errorf("failed to append to file: %s", err.Error())
	}

	return nil
}

func fsDel(ctx *gopherscript.Context, args ...interface{}) error {

	var fpath gopherscript.Path
	for _, arg := range args {
		switch v := arg.(type) {
		case gopherscript.Path:
			if fpath != "" {
				return errors.New(PATH_ARG_PROVIDED_TWICE)
			}
			fpath = v
		default:
			return errors.New("invalid argument " + fmt.Sprintf("%#v", v))
		}
	}

	if fpath == "" {
		return errors.New("missing path argument")
	}

	perm := gopherscript.FilesystemPermission{Kind_: gopherscript.DeletePerm, Entity: fpath}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return err
	}

	return os.RemoveAll(string(fpath))
}

type httpServer struct {
	endChan chan (interface{})
}

func (serv *httpServer) WaitClosed(ctx *gopherscript.Context) {
	<-serv.endChan
}

type httpRequest struct {
	Method  string
	URL     gopherscript.URL
	Path    gopherscript.Path
	Body    io.ReadCloser
	request *http.Request
}

type httpResponse struct {
	rw http.ResponseWriter
}

func (resp *httpResponse) WriteJSON(ctx *gopherscript.Context, v interface{}) (int, error) {

	var b []byte

	switch val := v.(type) {
	case []byte:
		b = val
	case string:
		b = []byte(val)
	default:
		b = []byte(toJSON(ctx, val))
	}
	if !json.Valid(b) {
		return 0, fmt.Errorf("not valid JSON : %s", string(b))
	}
	resp.rw.Header().Set("Content-Type", JSON_CTYPE)
	return resp.rw.Write(b)
}

func (resp *httpResponse) WriteHeader(ctx *gopherscript.Context, status int) {
	resp.rw.WriteHeader(status)
}

type httpRequestOptions struct {
	timeout            time.Duration
	InsecureSkipVerify bool
}

func checkHttpOptions(obj gopherscript.Object) (*httpRequestOptions, error) {
	options := *DEFAULT_HTTP_REQUEST_OPTIONS

	for k, v := range obj {

		//CHECK KEY

		if k == gopherscript.IMPLICIT_KEY_LEN_KEY {
			continue
		}

		_, err := strconv.ParseUint(k, 10, 32)
		if err != nil {
			return nil, errors.New("http option object: only integer keys are supported for now")
		}

		//CHECK VALUE

		v = gopherscript.UnwrapReflectVal(v)

		switch optVal := v.(type) {
		case gopherscript.QuantityRange:
			if options.timeout != DEFAULT_HTTP_CLIENT_TIMEOUT {
				return nil, errors.New("http option object: timeout already at least twice")
			}
			if d, ok := optVal.End.(time.Duration); ok {
				options.timeout = d
			} else {
				return nil, fmt.Errorf("invalid http option: a quantity range with end of type %T", optVal.End)
			}
		default:
			return nil, fmt.Errorf("invalid http option: %#v", optVal)
		}
	}

	return &options, nil
}

func getOrMakeHttpClient(opts *httpRequestOptions) *http.Client {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: opts.InsecureSkipVerify,
			},
		},
		Timeout: opts.timeout,
	}

	return client
}

func httpGet(ctx *gopherscript.Context, args ...interface{}) (*http.Response, error) {
	var contentType mimetype
	var URL gopherscript.URL
	var opts = DEFAULT_HTTP_REQUEST_OPTIONS

	for _, arg := range args {
		switch argVal := arg.(type) {
		case gopherscript.URL:
			URL = argVal
		case mimetype:
			contentType = argVal
		case gopherscript.Object:
			var err error
			opts, err = checkHttpOptions(argVal)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("invalid argument, type = %T ", arg)
		}
	}

	if URL == "" {
		return nil, errors.New(MISSING_URL_ARG)
	}

	perm := gopherscript.HttpPermission{
		Kind_:  gopherscript.ReadPerm,
		Entity: URL,
	}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return nil, err
	}

	ctx.Take(HTTP_REQUEST_RATE_LIMIT_NAME, 1)

	client := getOrMakeHttpClient(opts)
	req, err := http.NewRequest("GET", string(URL), nil)

	if contentType != "" {
		req.Header.Add("Accept", string(contentType))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to make request: %s", err.Error())
	}
	return client.Do(req)
}

func httpPost(ctx *gopherscript.Context, args ...interface{}) (*http.Response, error) {
	var contentType mimetype
	var URL gopherscript.URL
	var body io.Reader
	var opts = DEFAULT_HTTP_REQUEST_OPTIONS

	for _, arg := range args {
		switch argVal := arg.(type) {
		case gopherscript.URL:
			if URL != "" {
				return nil, errors.New("URL provided at least twice")
			}
			URL = argVal
		case mimetype:
			if contentType != "" {
				return nil, errors.New("MIME type provided at least twice")
			}
			contentType = argVal
		case string:
			if body != nil {
				return nil, errors.New("body provided at least twice")
			}
			body = strings.NewReader(argVal)
		case gopherscript.List:
			if body != nil {
				return nil, errors.New("body provided at least twice")
			}
			jsonString := toJSON(ctx, argVal)
			body = strings.NewReader(jsonString)
		case io.Reader:
			if body != nil {
				return nil, errors.New("body provided at least twice")
			}
			body = argVal
		case gopherscript.Object:

			if body == nil {
				jsonString := toJSON(ctx, argVal)
				body = strings.NewReader(jsonString)
			} else {

				if opts != DEFAULT_HTTP_REQUEST_OPTIONS {
					return nil, errors.New(HTTP_OPTION_OBJECT_PROVIDED_TWICE)
				}
				var err error
				opts, err = checkHttpOptions(argVal)
				if err != nil {
					return nil, err
				}
			}

		default:
			return nil, fmt.Errorf("only an URL argument is expected, not a(n) %T ", arg)
		}
	}

	if URL == "" {
		return nil, errors.New(MISSING_URL_ARG)
	}

	perm := gopherscript.HttpPermission{
		Kind_:  gopherscript.CreatePerm,
		Entity: URL,
	}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return nil, err
	}

	ctx.Take(HTTP_REQUEST_RATE_LIMIT_NAME, 1)

	client := getOrMakeHttpClient(opts)
	req, err := http.NewRequest("POST", string(URL), body)

	if contentType != "" {
		req.Header.Add("Content-Type", string(contentType))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to make request: %s", err.Error())
	}
	return client.Do(req)
}

func httpPatch(ctx *gopherscript.Context, args ...interface{}) (*http.Response, error) {
	var contentType mimetype
	var URL gopherscript.URL
	var body io.Reader
	var opts = DEFAULT_HTTP_REQUEST_OPTIONS

	for _, arg := range args {
		switch argVal := arg.(type) {
		case gopherscript.URL:
			if URL != "" {
				return nil, errors.New("URL provided at least twice")
			}
			URL = argVal
		case mimetype:
			if contentType != "" {
				return nil, errors.New("MIME type provided at least twice")
			}
			contentType = argVal
		case io.Reader:
			if body != nil {
				return nil, errors.New("body provided at least twice")
			}
			body = argVal
		case gopherscript.Object:
			if opts != nil {
				return nil, errors.New(HTTP_OPTION_OBJECT_PROVIDED_TWICE)
			}
			var err error
			opts, err = checkHttpOptions(argVal)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("only an URL argument is expected, not a(n) %T ", arg)
		}
	}

	if URL == "" {
		return nil, errors.New(MISSING_URL_ARG)
	}

	perm := gopherscript.HttpPermission{
		Kind_:  gopherscript.UpdatePerm,
		Entity: URL,
	}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return nil, err
	}

	ctx.Take(HTTP_REQUEST_RATE_LIMIT_NAME, 1)

	client := getOrMakeHttpClient(opts)
	req, err := http.NewRequest("PATCH", string(URL), body)

	if contentType != "" {
		req.Header.Add("Content-Type", string(contentType))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to make request: %s", err.Error())
	}
	return client.Do(req)
}

func httpDelete(ctx *gopherscript.Context, args ...interface{}) (*http.Response, error) {
	var URL gopherscript.URL
	var opts = DEFAULT_HTTP_REQUEST_OPTIONS

	for _, arg := range args {
		switch argVal := arg.(type) {
		case gopherscript.URL:
			if URL != "" {
				return nil, errors.New("URL provided at least twice")
			}
			URL = argVal
		case gopherscript.Object:
			if opts != nil {
				return nil, errors.New(HTTP_OPTION_OBJECT_PROVIDED_TWICE)
			}
			var err error
			opts, err = checkHttpOptions(argVal)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("only an URL argument is expected, not a(n) %T ", arg)
		}
	}

	if URL == "" {
		return nil, errors.New(MISSING_URL_ARG)
	}

	perm := gopherscript.HttpPermission{
		Kind_:  gopherscript.DeletePerm,
		Entity: URL,
	}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return nil, err
	}

	ctx.Take(HTTP_REQUEST_RATE_LIMIT_NAME, 1)

	client := getOrMakeHttpClient(opts)
	req, err := http.NewRequest("DELETE", string(URL), nil)

	if err != nil {
		return nil, fmt.Errorf("failed to make request: %s", err.Error())
	}
	return client.Do(req)
}

func makeHttpServer(addr string, handler http.Handler, certFilePath string, keyFilePath string) (*http.Server, string, string, error) {

	//we generate a self signed certificate that we write to disk so that
	//we can reuse it
	CERT_FILEPATH := "localhost.cert"
	CERT_KEY_FILEPATH := "localhost.key"

	certFile, err1 := os.Open(CERT_FILEPATH)
	keyFile, err2 := os.Open(CERT_KEY_FILEPATH)

	if errors.Is(err1, os.ErrNotExist) || errors.Is(err2, os.ErrNotExist) {

		if err1 == nil {
			certFile.Close()
			os.Remove(CERT_FILEPATH)
		}

		if err2 == nil {
			certFile.Close()
			os.Remove(CERT_KEY_FILEPATH)
		}

		cert, key, err := generateSelfSignedCertAndKey()
		if err != nil {
			return nil, "", "", err
		}

		certFile, err = os.Create(CERT_FILEPATH)
		if err != nil {
			return nil, "", "", err
		}
		pem.Encode(certFile, cert)
		certFile.Close()

		keyFile, err = os.Create(CERT_KEY_FILEPATH)
		if err != nil {
			return nil, "", "", err
		}
		pem.Encode(keyFile, key)
		keyFile.Close()
	}

	server := &http.Server{
		Addr:           addr,
		Handler:        handler,
		ReadTimeout:    8 * time.Second,
		WriteTimeout:   12 * time.Second,
		MaxHeaderBytes: 1 << 12,
	}

	return server, CERT_FILEPATH, CERT_KEY_FILEPATH, nil
}

func toJSON(ctx *gopherscript.Context, v interface{}) string {
	b, err := json.Marshal(v)

	if err != nil {
		log.Panicln("tojson:", err)
	}
	return string(b)
}

func toPrettyJSON(ctx *gopherscript.Context, v interface{}) string {
	b, err := json.MarshalIndent(v, "", " ")

	if err != nil {
		log.Panicln("tojson:", err)
	}
	return string(b)
}

func toJSONVal(ctx *gopherscript.Context, v interface{}) interface{} {

	s := toJSON(ctx, v)
	var jsonVal interface{}
	err := json.Unmarshal([]byte(s), &jsonVal)
	if err != nil {
		log.Panicln("from json:", err)
	}

	return jsonVal
}

func toGopherscriptVal(ctx *gopherscript.Context, v interface{}) interface{} {
	jsonVal := toJSONVal(ctx, v)

	return convertJSONValToGopherscriptVal(ctx, jsonVal)
}

func convertJSONValToGopherscriptVal(ctx *gopherscript.Context, v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		for key, value := range val {
			val[key] = toGopherscriptVal(ctx, value)
		}
		return gopherscript.Object(val)
	case []interface{}:
		for i, e := range val {
			val[i] = toGopherscriptVal(ctx, e)
		}
		return gopherscript.List(val)
	default:
		return val
	}
}

func parseJson(ctx *gopherscript.Context, v interface{}) (interface{}, error) {
	var b []byte

	switch val := v.(type) {
	case []byte:
		b = val
	case string:
		b = []byte(val)
	default:
		return "", fmt.Errorf("cannot parse non string|bytes: %T", val)
	}

	var result interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}

	return convertJSONValToGopherscriptVal(ctx, result), nil
}

func getPublicKey(privKey interface{}) interface{} {
	switch k := privKey.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}

func makePemBlockForKey(privKey interface{}) (*pem.Block, error) {
	switch k := privKey.(type) {
	case *rsa.PrivateKey:
		return &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(k),
		}, nil
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal ECDSA private key: %v", err)

		}
		return &pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: b,
		}, nil
	default:
		return nil, fmt.Errorf("cannot make PEM block for %#v", k)
	}
}

func generateSelfSignedCertAndKey() (cert *pem.Block, key *pem.Block, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Acme Co"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour * 24 * 180),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	template.DNSNames = append(template.DNSNames, "localhost")

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, getPublicKey(priv), priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %s", err)

	}

	keyBlock, err := makePemBlockForKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create key: %s", err)
	}

	return &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}, keyBlock, nil
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

func __createFile(ctx *gopherscript.Context, fpath gopherscript.Path, b []byte, fmode fs.FileMode) error {

	perm := gopherscript.FilesystemPermission{Kind_: gopherscript.CreatePerm, Entity: fpath.ToAbs()}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return err
	}

	ctx.Take(FS_TOTAL_NEW_FILE_LIMIT_NAME, 1)
	ctx.Take(FS_NEW_FILE_RATE_LIMIT_NAME, 1)

	rate, err := ctx.GetRate(FS_WRITE_LIMIT_NAME)
	if err != nil {
		return err
	}
	chunkSize := min(int(rate), min(len(b), max(FS_WRITE_MIN_CHUNK_SIZE, int(rate/10))))
	f, err := os.OpenFile(string(fpath), os.O_CREATE|os.O_WRONLY, fmode)
	if err != nil {
		return err
	}
	defer f.Close()

	for len(b) != 0 {
		ctx.Take(FS_WRITE_LIMIT_NAME, int64(chunkSize))

		_, err = f.Write(b[0:chunkSize])

		if err != nil {
			return err
		}
		b = b[chunkSize:]
		chunkSize = min(len(b), chunkSize)
	}

	return nil
}

func __readEntireFile(ctx *gopherscript.Context, fpath gopherscript.Path) ([]byte, error) {
	perm := gopherscript.FilesystemPermission{Kind_: gopherscript.ReadPerm, Entity: fpath.ToAbs()}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return nil, err
	}

	rate, err := ctx.GetRate(FS_READ_LIMIT_NAME)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(string(fpath))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, _ := f.Stat()

	chunk := make([]byte, min(int(rate), min(int(stat.Size()), max(FS_READ_MIN_CHUNK_SIZE, int(rate/10)))))

	var b []byte
	var totalN int64 = 0
	n := len(chunk)

	for {
		ctx.Take(FS_READ_LIMIT_NAME, int64(n))
		n, err = f.Read(chunk)
		if err != nil {
			return nil, err
		}

		b = append(b, chunk[0:n]...)
		totalN += int64(n)

		if totalN >= stat.Size() || err == io.EOF {
			break
		}
	}

	return b, nil
}

//----------

type SmallKVStore struct {
	inMemory   map[string]interface{}
	filepath   gopherscript.Path
	hasChanges bool
	ctx        *gopherscript.Context
	lock       sync.RWMutex
	closed     bool
}

func OpenOrCreateStore(ctx *gopherscript.Context, filepath gopherscript.Path) (*SmallKVStore, error) {
	store := &SmallKVStore{
		inMemory:   map[string]interface{}{},
		filepath:   filepath,
		hasChanges: false,
		ctx:        ctx,
		closed:     false,
	}

	if filepath.IsDirPath() {
		return nil, errors.New("open store: provide path has the shape of a directory path")
	}

	filepath = filepath.ToAbs()

	_, err := os.Stat(string(filepath))

	var b []byte
	if os.IsNotExist(err) {
		b = []byte("{}")
		if err := __createFile(ctx, filepath, b, DEFAULT_RW_FILE_FMODE); err != nil {
			return nil, err
		}
	} else {
		b, err = __readEntireFile(ctx, filepath)
		if err != nil {
			return nil, err
		}
	}

	if err := json.Unmarshal(b, &store.inMemory); err != nil {
		return nil, errors.New("open store: failed to parse JSON: " + err.Error())
	}

	timer := time.NewTicker(KV_STORE_PERSISTENCE_INTERVAL)

	go func() {
		for range timer.C {
			if store.closed {
				break
			}
			store.persist()
		}
	}()

	return store, nil
}

func (store *SmallKVStore) Set(ctx *gopherscript.Context, key string, value interface{}) {
	store.lock.Lock()
	defer store.lock.Unlock()

	store.inMemory[key] = value
	store.hasChanges = true
}

func (store *SmallKVStore) Get(ctx *gopherscript.Context, key string) (interface{}, bool) {
	store.lock.RLock()
	defer store.lock.RUnlock()

	v, ok := store.inMemory[key]
	return v, ok
}

func (store *SmallKVStore) Has(ctx *gopherscript.Context, key string) bool {
	store.lock.RLock()
	defer store.lock.RUnlock()

	_, ok := store.inMemory[key]
	return ok
}

func (store *SmallKVStore) persist() error {
	store.lock.Lock()
	defer store.lock.Unlock()

	if !store.hasChanges {
		return nil
	}

	b, err := json.Marshal(store.inMemory)
	if err != nil {
		return err
	}

	err = __createFile(store.ctx, store.filepath, b, DEFAULT_RW_FILE_FMODE)
	store.hasChanges = false

	return err
}

func (store *SmallKVStore) Close() {
	store.persist()
	store.closed = true
}

func _rand(ctx *gopherscript.Context, v interface{}) interface{} {

	switch val := v.(type) {
	case gopherscript.GenerativePattern:
		return val.Random()
	default:
		panic(fmt.Errorf("rand: cannot generate random value from argument of type %T", v))
	}
}
