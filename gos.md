# gos executable

The ``gos`` executable allows you to execute scripts & launch a REPL.
It provides a small set of functions to execute commands and do basic operations on files and HTTP resources.
Like Gopherscript it is not production read yet. If you find a bug of want to suggest a feature, create an issue please !

## Security & Minimalism

- ``gos`` is a single file with less than 2K lines and very few dependencies. Features will be added but the file will NEVER have tens of thousand of lines.
  It is and will stay easy to understand and audit. If you want more functionnality see the [executables](./README.md##executables) section in the README.
- the functions that have side effects (Golang functions) actively check permissions before doing anything.


## Installation

```
go install github.com/debloat-dev/Gopherscript/cmd/gos@v0.3.2
```

## Scripting

```
# hello.gos

require {
    use: {
        globals: "*"
    }
}

log "hello world !"
```

Execute the script with the ``run`` subcommand:

```
gos run hello.gos -p=required
```

## REPL

Launch the REPL with the ``repl`` subcommand:
```
gos repl
```

Before starting the REPL ``gos`` will execute ``~/startup.gos`` and grant the required permissions by the script to the REPL.\
No additional permissions will be granted. You can copy the file named ``startup.gos`` in this repository and modify it.


## Execute commands

```
ex echo "hello"   # 'ex echo hello' will not work
ex go help
```

NOTE: Almost no commands are allowed by default, edit your ``startup.gos`` file to allow more commands (and subcommands).


## Read, Create, Update, Delete, Provide resources

From now on we will references files, HTTP servers and endpoints as "resources".

You can easily manipulate resources using ``read | create | update | delete`` followed by the resource's name.\


### Read

Read the entries of a folder: ``read ./dir/``

Read a file: ``read ./file.txt``

Read an HTTP resource with: ``read https://debloat.dev/fakeapp/users/1``

### Create

Create a dir: ``create ./dir/``

Create a file: ``create ./file.txt [optional string content]``

### Update

Append to a file: ``update ./file.txt append <string>``

Patch an HTTP resource: ``update <url> <string | object>``

### Delete

Use ``delete <resource>`` for deletion. The deletion is recursive for folders.
