package main

import (
	"bufio"
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
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	gopherscript "github.com/debloat-dev/Gopherscript"

	//EXTERNAL
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

const BACKSPACE_CODE = 127
const CTRL_BACKSPACE_CODE = 23
const ENTER_CODE = 13
const CTRL_C_CODE = 3
const TAB_CODE = 9
const ESCAPE_CODE = 27
const PROMPT_LEN = 2

const DEFAULT_FILE_FMODE = fs.FileMode(0o400)
const DEFAULT_DIR_FMODE = fs.FileMode(0o500)
const DEFAULT_HTTP_CLIENT_TIMEOUT = 10 * time.Second

const PATH_ARG_PROVIDED_TWICE = "path argument provided at least twice"
const CONTENT_ARG_PROVIDED_TWICE = "content argument provided at least twice"
const MISSING_URL_ARG = "missing URL argument"
const HTTP_OPTION_OBJECT_PROVIDED_TWICE = "http option object provided at least twice"

var DEFAULT_HTTP_REQUEST_OPTIONS = &httpRequestOptions{
	timeout:            DEFAULT_HTTP_CLIENT_TIMEOUT,
	InsecureSkipVerify: true,
}

func writePrompt() {
	fmt.Print("> ")
}

func replaceNewLinesRawMode(s string) string {
	return strings.ReplaceAll(s, "\n", "\n\x1b[1E")
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
		case BACKSPACE_CODE:
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
			case 70:
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

func startShell(state *gopherscript.State) {
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

	writePrompt()

	reset := func() {
		input = nil
		backspaceCount = 0
		runeSeq = nil
		pressedTabCount = 0
	}

	moveCursorLineStart := func() {
		termenv.CursorBack(len(input) + PROMPT_LEN)
	}

	for {
		r, _, err := reader.ReadRune()

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
			termenv.CursorBack(len(input) + PROMPT_LEN)
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
			termenv.CursorBack(len(input) + PROMPT_LEN)

			writePrompt()
			fmt.Printf("%s", string(input))
			continue
		case Escape:
			continue
		case Backspace:

			if len(input) == 0 || backspaceCount >= len(input) {
				continue
			}

			start := len(input) - backspaceCount - 1
			right := input[start+1:]
			input = append(input[0:start], right...)

			termenv.CursorBack(1)
			termenv.ClearLineRight()
			fmt.Print(string(right))

			if len(right) != 0 {
				termenv.CursorBack(1)
			}

			continue
		case Home:
			prevBackspaceCount := backspaceCount
			backspaceCount = len(input)
			termenv.CursorBack(backspaceCount - prevBackspaceCount)
			continue
		case End:
			prevBackspaceCount := backspaceCount
			backspaceCount = 0
			termenv.CursorForward(prevBackspaceCount)
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
				fmt.Printf("\n\r")
				fmt.Printf("%s\n\r", strings.Join(names, " "))

				termenv.RestoreCursorPosition()
			}

		case Enter:
			history.Commands = append(history.Commands, string(input))

			inputString := string(input)

			fmt.Print("\n\r")
			termenv.ClearLine()
			termenv.CursorNextLine(1)

			switch inputString {
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

			writePrompt()
			continue
		}

		if strconv.IsPrint(r) {
			termenv.ClearLine()
			moveCursorLineStart()
			input = append(left, r)
			input = append(input, right...)

			writePrompt()
			fmt.Print(string(input))

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
		if args[i][0] == '-' {
			temp := args[i]
			args[i] = args[index]
			args[index] = temp
			index++
		}
	}
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
				panic(fmt.Sprint("parsing error:", err.Error()))
			}

			if err := gopherscript.Check(mod); err != nil {
				panic(fmt.Sprint("checking error:", err.Error()))
			}

			var ctx *gopherscript.Context
			if mod.Requirements == nil {
				panic("missing requirements in script")
			}
			requiredPermissions := mod.Requirements.Object.Permissions(mod.GlobalConstantDeclarations, nil)

			if perms == "required" {
				ctx = gopherscript.NewContext(requiredPermissions)
			} else if len(requiredPermissions) != 0 {
				panic("some required permissions are not granted. Did you use -p=required ?")
			}

			//CONTEXT & STATE

			state := NewState(ctx)

			//EXECUTION

			_, err = gopherscript.Eval(mod, state)
			if err != nil {
				fmt.Print(err, "\n")
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

			b, err := os.ReadFile(startupScriptPath)
			if err != nil {
				panic(fmt.Sprint("failed to read startup file ", startupScriptPath, ":", err))
			}

			startupMod, err := gopherscript.ParseAndCheckModule(string(b), "")
			if err != nil {
				log.Panicln("failed to parse & check startup file:", err)
			}
			requiredPermissions := startupMod.Requirements.Object.Permissions(startupMod.GlobalConstantDeclarations, nil)
			ctx := gopherscript.NewContext(requiredPermissions)
			state := NewState(ctx)

			_, err = gopherscript.Eval(startupMod, state)
			if err != nil {
				panic(fmt.Sprint("startup script failed:", err))
			}
			startShell(state)
		default:
			panic(fmt.Sprint("unknown subcommand", os.Args[1]))
		}
	}
}

type FileInfo struct {
	Name    string      // base name of the file
	Size    int64       // length in bytes for regular files; system-dependent for others
	Mode    os.FileMode // file mode bits
	ModTime time.Time   // modification time
	IsDir   bool        // abbreviation for Mode().IsDir()
}

func NewState(ctx *gopherscript.Context) *gopherscript.State {

	var state *gopherscript.State
	state = gopherscript.NewState(ctx, map[string]interface{}{
		"fs": gopherscript.Object{
			"mkfile": gopherscript.ValOf(fsMkfile),
			"mkdir":  gopherscript.ValOf(fsMkdir),
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
							state := NewState(ctx)

							req := httpRequest{
								Method: r.Method,
								URL:    gopherscript.URL(r.URL.String()),
								Path:   gopherscript.Path(r.URL.Path),
								Body:   r.Body,
							}

							log.Println(replaceNewLinesRawMode(fmt.Sprintf("%#v", req)))

							resp := &httpResponse{
								rw: w,
							}

							_, err := gopherscript.CallFunc(v, state, gopherscript.List{
								gopherscript.ValOf(resp),
								gopherscript.ValOf(req),
							}, false)

							if err != nil {
								log.Println(err)
								termenv.CursorNextLine(1)
							}
						})

					case reflect.Value:

					}
				}

				if addr == "" {
					return nil, errors.New("no address required")
				}

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
		"map": func(ctx *gopherscript.Context, node gopherscript.Node, list gopherscript.List) (gopherscript.List, error) {
			result := gopherscript.List{}

			state.PushScope()
			defer state.PopScope()

			for _, e := range list {
				state.CurrentScope()[""] = e
				res, err := gopherscript.Eval(node.(*gopherscript.LazyExpression).Expression, state)
				if err != nil {
					return nil, err
				}
				result = append(result, res)
			}

			return result, nil
		},
		"filter": func(ctx *gopherscript.Context, node gopherscript.Node, list gopherscript.List) (gopherscript.List, error) {
			result := gopherscript.List{}

			state.PushScope()
			defer state.PopScope()

			for _, e := range list {
				state.CurrentScope()[""] = e
				res, err := gopherscript.Eval(node.(*gopherscript.LazyExpression).Expression, state)
				if err != nil {
					return nil, err
				}
				if res.(bool) {
					result = append(result, e)
				}
			}

			return result, nil
		},
		"tojson":    toJSON,
		"topjson":   toPrettyJSON,
		"tojsonval": toJSONVal,
		"toval":     toGopherscriptVal,
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
					case "application/json", "text/html", "text/plain":
						return string(b), nil
					}

					return b, nil
				}
			case gopherscript.Path:
				if res.IsDirPath() {
					return fsLs(ctx, res)
				} else {
					b, err := os.ReadFile(string(res))
					if err != nil {
						return nil, err
					}
					switch contentType {
					case "application/json", "text/html", "text/plain":
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
					case "application/json", "text/html", "text/plain":
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
					case "application/json", "text/html", "text/plain":
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
					case "application/json", "text/html", "text/plain":
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
	})

	return state
}

func fsLs(ctx *gopherscript.Context, args ...interface{}) ([]FileInfo, error) {
	var pth gopherscript.Path
	var patt gopherscript.PathPattern
	ERR := "only a single path (or path pattern) argument is expected"

	for _, arg := range args {
		switch v := arg.(type) {
		case gopherscript.Path:
			if pth != "" {
				return nil, errors.New(ERR)
			}
			pth = v.ToAbs()
			if !v.IsDirPath() {
				return nil, errors.New("only directory paths are supported : " + string(v))
			}
		case gopherscript.PathPattern:
			if patt != "" {
				return nil, errors.New(ERR)
			}
			patt = v
		default:
			return nil, errors.New("invalid argument " + fmt.Sprintf("%#v", v))
		}
	}

	if pth != "" && patt != "" {
		return nil, errors.New(ERR)
	}

	fileInfo := make([]fs.FileInfo, 0)
	resultFileInfo := make([]FileInfo, 0)

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
			fileInfo = append(fileInfo, info)
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
			fileInfo = append(fileInfo, info)
		}
	}

	for _, info := range fileInfo {
		resultFileInfo = append(resultFileInfo, FileInfo{
			Name:    info.Name(),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
			IsDir:   info.IsDir(),
		})
	}

	return resultFileInfo, nil

}

type mimetype string

func mime_(ctx *gopherscript.Context, arg string) (mimetype, error) {
	switch arg {
	case "json":
		arg = "application/json"
	case "text":
		arg = "text/plain"
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
	var content string

	for _, arg := range args {
		switch v := arg.(type) {
		case gopherscript.Path:
			if fpath != "" {
				return errors.New(PATH_ARG_PROVIDED_TWICE)
			}
			fpath = v
		case string:
			if content != "" {
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

	perm := gopherscript.FilesystemPermission{Kind_: gopherscript.CreatePerm, Entity: fpath}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return err
	}

	return os.WriteFile(string(fpath), []byte(content), DEFAULT_FILE_FMODE)
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
	Method string
	URL    gopherscript.URL
	Path   gopherscript.Path
	Body   io.ReadCloser
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
	resp.rw.Header().Set("Content-Type", "application/json")
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
		Kind_:  gopherscript.CreatePerm,
		Entity: URL,
	}
	if err := ctx.CheckHasPermission(perm); err != nil {
		return nil, err
	}

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

	client := getOrMakeHttpClient(opts)
	req, err := http.NewRequest("DELETE", string(URL), nil)

	if err != nil {
		return nil, fmt.Errorf("failed to make request: %s", err.Error())
	}
	return client.Do(req)
}

func makeHttpServer(addr string, handler http.Handler, certFilePath string, keyFilePath string) (*http.Server, string, string, error) {

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
