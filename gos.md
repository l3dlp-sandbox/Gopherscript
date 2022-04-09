# gos executable

The ``gos`` executable allows you to execute scripts & launch a REPL.
It provides a small set of functions to do basic operations on files and HTTP resources.
Like Gopherscript it is not production read yet. If you find a bug of want to suggest a feature, create an issue please !

## Security & Minimalism

- ``gos`` is a single file with less than 2K lines and very few dependencies. Features will be added but the file will NEVER have tens of thousand of lines.
  It is and will stay easy to understand and audit. If you want more functionnality see the [executables](./README.md##executables) section in the README.
- the functions that have side effects (Golang functions) actively checks permissions before doing anything.

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

Before starting the REPL ``gos`` will execute ``./startup.gos`` and grant the required permissions by the script to the REPL.\
No additional permissions will be granted.


## Read, Create, Delete, Provide resources

From now on we will references files, HTTP servers and endpoints as "resources".

You can easily manipulate resources using ``read | create | delete`` followed by the resource's name.\
To create a folder do ``create ./dir/`` (trailing slash required), for a file do ``create ./file.txt [optional string content]``.\
Use ``delete <resource>`` for deletion, if the resource is a folder the deletion will be recursive.\
Read a file's content with ``read ./file.txt`` or read an HTTP resource with ``read https://example.com/data.json``
