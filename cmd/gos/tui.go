package main

import (
	"errors"
	"fmt"
	"log"

	gopherscript "github.com/debloat-dev/Gopherscript"
	tcell "github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func newTuiNamespace(state *gopherscript.State) gopherscript.Object {
	return gopherscript.Object{
		"app": gopherscript.ValOf(func(ctx *gopherscript.Context, config gopherscript.Object) (*tview.Application, error) {

			root, ok := config["root"]
			if !ok {
				return nil, errors.New("missing application's root")
			}

			root = gopherscript.UnwrapReflectVal(root)

			rootPrim, isPrimitive := root.(tview.Primitive)
			if !isPrimitive {
				return nil, fmt.Errorf("application's root is not a primitive, it's a(n) %T", root)
			}

			return tview.NewApplication().EnableMouse(true).SetRoot(rootPrim, true), nil
		}),

		"text": gopherscript.ValOf(func(ctx *gopherscript.Context, text string) *tview.TextView {

			textView := tview.NewTextView().
				SetDynamicColors(true).
				SetRegions(true).
				SetWordWrap(true)

			textView.SetText(text)
			return textView
		}),
		"flex": gopherscript.ValOf((func(ctx *gopherscript.Context, config gopherscript.Object) *tview.Flex {
			flex := tview.NewFlex()

			it := config.Indexed()
			for it.HasNext(nil) {
				value := it.GetNext(nil)
				value = gopherscript.UnwrapReflectVal(value)

				switch val := value.(type) {
				case tview.Primitive:
					flex.AddItem(val, 0, 1, true)
				default:
				}
			}

			return flex
		})),
		"tree-node": gopherscript.ValOf(func(ctx *gopherscript.Context, config gopherscript.Object) (*tview.TreeNode, error) {
			text, ok := config["text"]
			if !ok {
				text = "<node>"
			}
			actualText := fmt.Sprint(text)

			ref, ok := config["ref"]
			if !ok {
				ref = nil
			}

			node := tview.NewTreeNode(actualText).
				SetReference(ref).
				SetSelectable(true)

			return node, nil
		}),
		"tree": gopherscript.ValOf(func(ctx *gopherscript.Context, config gopherscript.Object) (tview.Primitive, error) {
			rootDir := "."
			root := tview.NewTreeNode(rootDir).
				SetColor(tcell.ColorRed)
			tree := tview.NewTreeView().
				SetRoot(root).
				SetCurrentNode(root)

			setup, ok := config["setup"]
			if ok {
				switch fn := setup.(type) {
				case gopherscript.Func:
					nodes, err := gopherscript.CallFunc(fn, state, gopherscript.List{}, false)
					if err == nil {
						for _, node := range nodes.(gopherscript.List) {
							node = gopherscript.UnwrapReflectVal(node)
							root.AddChild(node.(*tview.TreeNode))
						}

					} else {
						log.Println(err)
					}
				default:
				}
			}

			onSelectionItem, ok := config["on-selection"]
			if ok {
				switch fn := onSelectionItem.(type) {
				case gopherscript.Func:
					tree.SetSelectedFunc(func(node *tview.TreeNode) {
						gopherscript.CallFunc(fn, state, gopherscript.List{nil, nil}, false)
					})
				default:
				}
			}

			return tree, nil
		}),
	}
}
