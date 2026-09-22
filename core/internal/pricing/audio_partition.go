package pricing

import (
	"math/big"

	"github.com/expr-lang/expr/ast"
)

// audioSplitIndependent proves invariance under text -= x, audio += x using
// exact rational coefficients. Unsupported/nonlinear dependencies fail closed;
// sampling two counts would miss conditional or nonlinear audio surcharges.
func (e *expression) audioSplitIndependent(text, audio string) bool {
	literal := func(node ast.Node) (*big.Rat, bool) {
		switch node.(type) {
		case *ast.IntegerNode, *ast.FloatNode:
			loc := node.Location()
			return new(big.Rat).SetString(string(e.source[loc.From:loc.To]))
		}
		return nil, false
	}
	var slope func(ast.Node) (*big.Rat, bool)
	slope = func(node ast.Node) (*big.Rat, bool) {
		zero := new(big.Rat)
		switch n := node.(type) {
		case *ast.IntegerNode, *ast.FloatNode, *ast.StringNode, *ast.BoolNode:
			return zero, true
		case *ast.IdentifierNode:
			if n.Value == text {
				return big.NewRat(-1, 1), true
			}
			if n.Value == audio {
				return big.NewRat(1, 1), true
			}
			return zero, true
		case *ast.BinaryNode:
			left, lok := slope(n.Left)
			right, rok := slope(n.Right)
			if !lok || !rok {
				return nil, false
			}
			if left.Sign() == 0 && right.Sign() == 0 {
				return zero, true
			}
			switch n.Operator {
			case "+":
				return zero.Add(left, right), true
			case "-":
				return zero.Sub(left, right), true
			case "*":
				if rate, ok := literal(n.Right); ok {
					return zero.Mul(left, rate), true
				}
				if rate, ok := literal(n.Left); ok {
					return zero.Mul(right, rate), true
				}
			case "/":
				if rate, ok := literal(n.Right); ok && rate.Sign() != 0 {
					return zero.Quo(left, rate), true
				}
			}
		case *ast.ConditionalNode:
			condition, ok := slope(n.Cond)
			if !ok || condition.Sign() != 0 {
				return nil, false
			}
			left, lok := slope(n.Exp1)
			right, rok := slope(n.Exp2)
			if lok && rok && left.Cmp(right) == 0 {
				return left, true
			}
		case *ast.CallNode:
			name := n.Callee.(*ast.IdentifierNode).Value
			if name == "tier" {
				label, ok := slope(n.Arguments[0])
				if !ok || label.Sign() != 0 {
					return nil, false
				}
				return slope(n.Arguments[1])
			}
			value, ok := slope(n.Arguments[0])
			if ok && name == "fixed" {
				return zero.Mul(value, big.NewRat(1000000, 1)), true
			}
			if ok && value.Sign() == 0 {
				return zero, true
			}
		}
		return nil, false
	}
	value, ok := slope(e.root)
	return ok && value.Sign() == 0
}
