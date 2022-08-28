package tui

import (
	"errors"
	"fmt"
	"log"

	gopherscript "github.com/debloat-dev/Gopherscript"
	tcell "github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type treeNode struct {
	actualNode *tview.TreeNode
}

func (node treeNode) GetData(*gopherscript.Context) interface{} {
	return node.actualNode.GetReference()
}

func (node treeNode) AddChild(ctx *gopherscript.Context, child treeNode) interface{} {
	return node.actualNode.AddChild(child.actualNode)
}

func (node treeNode) AddChildren(ctx *gopherscript.Context, children gopherscript.List) {
	for _, child := range children {
		node.actualNode.AddChild(child.(treeNode).actualNode)
	}
}

func (node treeNode) GetChildren(ctx *gopherscript.Context) gopherscript.List {
	list := gopherscript.List{}

	for _, c := range node.actualNode.GetChildren() {
		list = append(list, treeNode{c})
	}

	return list
}

func (node treeNode) RemoveAllChildren(ctx *gopherscript.Context) {
	for _, child := range node.GetChildren(ctx) {
		node.actualNode.RemoveChild(child.(treeNode).actualNode)
	}
}

func (node treeNode) Collapse(ctx *gopherscript.Context) {
	node.actualNode.Collapse()
}

func (node treeNode) Expand(ctx *gopherscript.Context) {
	node.actualNode.Expand()
}

func NewTuiNamespace(state *gopherscript.State) gopherscript.Object {
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
		"flex": gopherscript.ValOf((func(ctx *gopherscript.Context, config gopherscript.Object) (*tview.Flex, error) {
			flex := tview.NewFlex()

			var itemRelSizes []int

			switch sizes := config["sizes"].(type) {
			case gopherscript.List:
				for _, e := range sizes {
					integer, ok := e.(int)
					if !ok {
						return nil, errors.New("invalid configuration for flex element: .sizes should be a list of integers")
					}
					itemRelSizes = append(itemRelSizes, integer)
				}
			}

			if len(itemRelSizes) != config.IndexedItemCount() {
				return nil, errors.New("invalid configuration for flex element: the length of .sizes should be equal to the number of child elements")
			}

			it := config.Indexed()

			for i := 0; it.HasNext(nil); i++ {
				value := it.GetNext(nil)
				value = gopherscript.UnwrapReflectVal(value)

				switch val := value.(type) {
				case tview.Primitive:
					flex.AddItem(val, 0, itemRelSizes[i], true)
				default:
				}
			}

			switch config["direction"] {
			case gopherscript.Identifier("column"):
				flex.SetDirection(tview.FlexColumnCSS)
			case gopherscript.Identifier("row"):
				flex.SetDirection(tview.FlexRowCSS)
			}

			return flex, nil
		})),
		"tree-node": gopherscript.ValOf(func(ctx *gopherscript.Context, config gopherscript.Object) (treeNode, error) {
			text, ok := config["text"]
			if !ok {
				text = "<node>"
			}
			actualText := fmt.Sprint(text)

			data, ok := config["data"]
			if !ok {
				data = nil
			}

			node := tview.NewTreeNode(actualText).
				SetReference(data).
				SetSelectable(true)

			return treeNode{node}, nil
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
							treeNode_, ok := node.(treeNode)
							if !ok {
								//TODO: print error message instead
								panic("the setup() function should return a list of tree nodes")
							}
							root.AddChild(treeNode_.actualNode)
						}

					} else {
						log.Println(err)
					}
				default:
				}
			}

			onSelection, ok := config["on-selection"]
			if ok {
				switch fn := onSelection.(type) {
				case gopherscript.Func:
					tree.SetSelectedFunc(func(node *tview.TreeNode) {
						_, err := gopherscript.CallFunc(fn, state, gopherscript.List{nil, gopherscript.ValOf(treeNode{node})}, false)
						if err != nil {
							log.Println(err)
						}
					})
				default:
				}
			}

			return tree, nil
		}),
	}
}
