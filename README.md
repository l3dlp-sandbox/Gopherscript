# Gopherscript

Gopherscript is a secure scripting/configuration language written in Go. 
It features a fined-grain permission system and enforces a strong isolation of dependencies.
Gopherscript is not production ready yet : if you find a bug or want to suggest a feature create an issue please !

Join the official community on [Revolt](https://app.revolt.chat/invite/cJQPeQkc).
If you prefer to use Discord I am active in this [server about Golang](https://discord.gg/dZwwfECx).

## Security & Minimalism

- The codebase is small on purpose (a single Go file with less than 6K lines and only std lib dependencies). See [Implementation](#implementation).
- The default global scope has ZERO variables/functions and there are no "builtin modules" to import. (only add what you need from Golang)
- A strict but extensive permission system allows you to precisely control what is allowed (almost no permissions by default). 
  For more details go to the [permission section](#permissions).
- Paths, path patterns, URLs are literals and dynamic paths are only possible as path expressions. You cannot create them from strings at runtime ! That facilitates the listing of permissions and helps static analysis.
- Properties cannot be accessed with a dynamic name ( ``$obj[$name]`` ), only Go functions that are passed objects in can (you have to trust them anyway).
- In a general way some features will be added (or refused) to limit code obfuscation.

If you find Gopherscript too restrictive don't worry ! A ``lax`` mode might be introduced in the future.

## Installation & Usage

You can use the ``gos`` executable to execute scripts or launch a REPL:
```
go install github.com/debloat-dev/Gopherscript/cmd/gos@v0.1.2
```
See the documentation [here](./gos.md).
You can also use Gopherscript as a library and only add whay you need to the global scope (see the example further below).


### Editor support

If you use VSCode you can install the extension of ID ``xbsd.gopherscript`` . If you are a NeoVim user, check this [repo](https://github.com/debloat-dev/Gopherscript-nvim) please.

### Example 

Example of using Gopherscript as a library from Golang:

```go
package main

import (
	gos "github.com/debloat-dev/Gopherscript"
	"log"
)

type User struct {
	Name string
}

func main() {
   	//we create a Context that contains the granted permissions
	grantedPerms := []gos.Permission{
		gos.GlobalVarPermission{gos.UsePerm, "*"},
	}
	ctx := gos.NewContext(grantedPerms)

    	//we create the initial state with the globals we want to expose
    	//the state can be re used several times (and with different modules)
	state := gos.NewState(ctx, map[string]interface{}{
		"makeUser": func(ctx *gos.Context) User {
			return User{Name: "Bar"}
		},
	})

	mod, err := gos.ParseAndCheckModule(`
            # permissions must be requested at the top of the file AND granted
            require {
                use: {globals: "*"} 
            }
            $a = 1
            $user = makeUser()
            return [
                ($a + 2),
                $user.Name
            ]
        `, "")
	
	if err != nil {
		log.Panicln(err)
	}

	//we execute the script
	res, err := gos.Eval(mod, state)
	if err != nil {
		log.Panicln(err)
	}

	log.Printf("%#v", res)
}
```

You can learn more about the interactions between Gopherscript and Golang [here](https://github.com/debloat-dev/Gopherscript/wiki/Advanced#interaction-with-golang).


## Features

The most important features are described in this section. If you want to learn Gopherscript or want to know more details about specific features you can go on the wiki.

### Basic

![image](https://user-images.githubusercontent.com/84961291/163572856-5ca4ddd5-3bd0-44d6-a73e-efde341a9bd7.png)

<!--

integer = 1 
float = 1.0

if true {
    log 1 2
} else {
    log(3,4)
}

slice1 = ["a", "b", 3, $integer]
slice2 = [
    "a" 
    "b"
    3
    $integer
]

object = {name: "Foo"}
-->

### Permissions

Required permissions are specified at the top of each module (file).

![image](https://user-images.githubusercontent.com/84961291/162411317-58513db5-87c3-4f9d-a5f2-7204a0951303.png)

<!--
require {
    read: {
        # access to all HTTPS hosts (GET, HEAD, QUERY)
        : https://*

        # access to all paths prefixed with /home/project
        : /home/project/...
    }
    use: {
        globals: "*"
    }
    create: {
        globals: ["myvar"]
        : https://* # POST to all HTTPS hosts
    }
}
-->


There are several permission kinds: Create, Update, Read, Delete, Use, Consume, Provide.
Some permission types are already provided: FilesystemPermission, HttpPermission, StackPermission, GlobalVarPermission.
You can specify your own permissions by implementing the Permission interface

```go
type Permission interface {
	Kind() PermissionKind
	Includes(Permission) bool
}
```

### Special literals & expressions

```
# Path literals
/home/user/
./file.json

# Path expressions
/home/user/$dirpath

# Path patterns support basic globbing (*, [set], ?) and prefixing (not both at the same time).
./data/*.json
/app/logs/...
/app/*/...     		# invalid (this might be allowed in the future though)

# HTTP host literals
https://example.com
https://example.com:443

# HTTP host pattern literals
https://*               # any HTTPS host
https://*.com           # any domain with .com as TLD, will not match against subdomains
https://*.example.com   # any subdomain of example.com

# URL literals
https://example.com/
https://example.com/index.html
https://localhost/

# URL expressions
https://example.com/users/$id

# URL pattern literals (only prefix patterns supported)
https://example.com/users/...
```

### Quantity literals

```
10s		# time.Duration
10ms		# time.Duration
10%		# 0.10

sleep 100ms
```

### Functions

```
fn myfunc($x){
    return ($x + 1)
}

$y = myfunc(1)
myfunc 1 { }
myfunc 1 { 

}
```

### Imports

Syntax:
```
import <varname> <url> <file-content-sha256>  { <global variables> } allow { <permission> }
```

Importing a module is like executing a script with the passed globals and granted permissions.

```
# content of https://example.com/return_1.gos
return 1
```

Script:

![image](https://user-images.githubusercontent.com/84961291/162414629-e6426d1c-e135-4bbf-aee6-1b778817cbf6.png)

<!--
import modresult https://example.com/return_1.gos "SG2a/7YNuwBjsD2OI6bM9jZM4gPcOp9W8g51DrQeyt4=" {MY_GLOBVAR: "a"} allow {}
-->

### Routines

Routines are mainly used for concurrent work and isolation. Each routine has its own goroutine and state.

Syntax for spawning routines:
```
$routine = sr [group] <globals> <module | call | variable>
``` 

Call (all permissions are inherited).
```
$routine = sr nil f()
```

Embedded module:

![image](https://user-images.githubusercontent.com/84961291/162414775-75d0562c-0e99-402f-8a66-b85fdb730a09.png)

<!--
$routine = sr {http: $$http} {
    return http.get(https://example.com/)!
} allow { 
    use: {globals: ["http"]} 
}
-->

You can wait for the routine's result by calling the WaitResult method:
```
$result = $routine.WaitResult()!
```

Routines can optionally be part of a "routine group" that allows easier control of multiple routines. The group variable is defined (and updated) when the spawn expression is evaluated.

```
for (1 .. 10) {
    sr req_group nil read(https://debloat.dev/fakeapp/users)!
}

$results = $req_group.WaitAllResults()!
```

**For more details about the different features you can read the repository's wiki.**

## Implementation

- Why use a tree walk interpreter instead of a bytecode interpreter ?\
  -> Tree walk interpreters are slower but simpler : that means less bugs and vulnerabilities. A Gopherscript implementation that uses a bytecode interpreter might be developed in the future though.

## Executables

When something tries to do too many things it becomes impossible to understand for the average developper. That makes audits and customization harder.
Goperscript aims to have a different model. Depending on the features you need, you install one one more executables that try to do one thing well without uncessary bloat (each one providing specific globals to Gophercript).
