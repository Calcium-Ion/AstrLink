package pricing

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// Expressions are parsed, never executed as programs. Only this small AST and
// these pure billing functions are accepted; decimal literals use their source
// spelling and all arithmetic is rational until the final 9-decimal rounding.
type expression struct {
	source []rune
	root   ast.Node
	vars   map[string]bool
}

func parseExpression(source string) (*expression, error) {
	if len(source) == 0 || len(source) > 16384 {
		return nil, fmt.Errorf("invalid price expression length")
	}
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("invalid price expression")
	}
	e := &expression{source: []rune(source), root: tree.Node, vars: map[string]bool{}}
	nodes := 0
	var check func(ast.Node, int) error
	check = func(node ast.Node, depth int) error {
		nodes++
		if nodes > 2048 || depth > 64 {
			return fmt.Errorf("price expression too complex")
		}
		switch n := node.(type) {
		case *ast.IntegerNode, *ast.FloatNode, *ast.StringNode, *ast.BoolNode:
			return nil
		case *ast.IdentifierNode:
			if !strings.Contains("|p|c|cr|cc|cc1h|ai|ao|len|image_count|", "|"+n.Value+"|") {
				return fmt.Errorf("unsupported billing variable")
			}
			e.vars[n.Value] = true
		case *ast.BinaryNode:
			if !strings.Contains("|+|-|*|/|==|!=|<|<=|>|>=|&&||||", "|"+n.Operator+"|") {
				return fmt.Errorf("unsupported billing operator")
			}
			if err := check(n.Left, depth+1); err != nil {
				return err
			}
			return check(n.Right, depth+1)
		case *ast.ConditionalNode:
			for _, v := range []ast.Node{n.Cond, n.Exp1, n.Exp2} {
				if err := check(v, depth+1); err != nil {
					return err
				}
			}
		case *ast.CallNode:
			name, ok := n.Callee.(*ast.IdentifierNode)
			if !ok {
				return fmt.Errorf("unsupported billing call")
			}
			arity := map[string]int{"tier": 2, "fixed": 1, "param": 1, "weekday": 1, "hour": 1, "minute": 1}[name.Value]
			if arity == 0 || len(n.Arguments) != arity {
				return fmt.Errorf("unsupported billing function")
			}
			for _, v := range n.Arguments {
				if err := check(v, depth+1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unsupported billing syntax")
		}
		return nil
	}
	if err = check(tree.Node, 0); err != nil {
		return nil, err
	}
	return e, nil
}
func ValidateExpression(s string) error { _, err := parseExpression(s); return err }

type Valuation struct {
	AmountUSD string `json:"amount_usd"`
	Tier      string `json:"tier"`
}

func Evaluate(source string, u *contract.Usage, at time.Time) (Valuation, error) {
	e, err := parseExpression(source)
	if err != nil {
		return Valuation{}, err
	}
	if u == nil {
		return Valuation{}, fmt.Errorf("missing_usage")
	}
	if u.BillingIncomplete {
		return Valuation{}, fmt.Errorf("incomplete_usage")
	}
	if err = u.Validate(); err != nil {
		return Valuation{}, fmt.Errorf("invalid_usage")
	}
	count := func(v *int) int64 {
		if v == nil {
			return 0
		}
		return int64(*v)
	}
	in, out := int64(u.InputTokens), int64(u.OutputTokens)
	read, write, oneHour := count(u.CacheReadTokens), count(u.CacheWriteTokens), count(u.CacheWrite1hTokens)
	if read+write > in || oneHour > write {
		return Valuation{}, fmt.Errorf("invalid_cache_partition")
	}
	if write > 0 && e.vars["cc1h"] && u.CacheWrite1hTokens == nil {
		return Valuation{}, fmt.Errorf("missing_cache_ttl")
	}
	if e.vars["cr"] {
		in -= read
	}
	if e.vars["cc"] || e.vars["cc1h"] {
		in -= write
	}
	if e.vars["ai"] && u.InputAudioTokens == nil || e.vars["ao"] && u.OutputAudioTokens == nil {
		return Valuation{}, fmt.Errorf("missing_audio_usage")
	}
	audioIn, audioOut := count(u.InputAudioTokens), count(u.OutputAudioTokens)
	if e.vars["ai"] {
		in -= audioIn
	}
	if e.vars["ao"] {
		out -= audioOut
	}
	if in < 0 || out < 0 {
		return Valuation{}, fmt.Errorf("invalid_token_partition")
	}
	if !e.vars["cc1h"] {
		oneHour = 0
	}
	if read > 0 && audioIn > 0 && e.vars["cr"] && e.vars["ai"] {
		return Valuation{}, fmt.Errorf("missing_audio_cache_partition")
	}
	values := map[string]int64{"p": in, "c": out, "cr": read, "cc": write - oneHour, "cc1h": oneHour, "ai": audioIn, "ao": audioOut, "len": int64(u.InputTokens)}
	tier := ""
	var eval func(ast.Node) (any, error)
	num := func(v any) (*big.Rat, error) {
		r, ok := v.(*big.Rat)
		if !ok {
			return nil, fmt.Errorf("invalid numeric expression")
		}
		return r, nil
	}
	eval = func(node ast.Node) (any, error) {
		switch n := node.(type) {
		case *ast.IntegerNode, *ast.FloatNode:
			loc := node.Location()
			v, ok := new(big.Rat).SetString(string(e.source[loc.From:loc.To]))
			if !ok {
				return nil, fmt.Errorf("invalid decimal")
			}
			return v, nil
		case *ast.StringNode:
			return n.Value, nil
		case *ast.BoolNode:
			return n.Value, nil
		case *ast.IdentifierNode:
			v, ok := values[n.Value]
			if !ok {
				return nil, fmt.Errorf("missing_%s", n.Value)
			}
			return new(big.Rat).SetInt64(v), nil
		case *ast.ConditionalNode:
			v, err := eval(n.Cond)
			if err != nil {
				return nil, err
			}
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("invalid condition")
			}
			if b {
				return eval(n.Exp1)
			}
			return eval(n.Exp2)
		case *ast.BinaryNode:
			l, err := eval(n.Left)
			if err != nil {
				return nil, err
			}
			if n.Operator == "&&" || n.Operator == "||" {
				b, ok := l.(bool)
				if !ok {
					return nil, fmt.Errorf("invalid boolean")
				}
				if n.Operator == "&&" && !b || n.Operator == "||" && b {
					return b, nil
				}
				return eval(n.Right)
			}
			r, err := eval(n.Right)
			if err != nil {
				return nil, err
			}
			if n.Operator == "==" || n.Operator == "!=" {
				equal := false
				switch a := l.(type) {
				case *big.Rat:
					b, ok := r.(*big.Rat)
					equal = ok && a.Cmp(b) == 0
				case string:
					b, ok := r.(string)
					equal = ok && a == b
				case bool:
					b, ok := r.(bool)
					equal = ok && a == b
				}
				if n.Operator == "!=" {
					equal = !equal
				}
				return equal, nil
			}
			a, err := num(l)
			if err != nil {
				return nil, err
			}
			b, err := num(r)
			if err != nil {
				return nil, err
			}
			v := new(big.Rat)
			switch n.Operator {
			case "+":
				return v.Add(a, b), nil
			case "-":
				return v.Sub(a, b), nil
			case "*":
				return v.Mul(a, b), nil
			case "/":
				if b.Sign() == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				return v.Quo(a, b), nil
			case "<":
				return a.Cmp(b) < 0, nil
			case "<=":
				return a.Cmp(b) <= 0, nil
			case ">":
				return a.Cmp(b) > 0, nil
			case ">=":
				return a.Cmp(b) >= 0, nil
			}
		case *ast.CallNode:
			name := n.Callee.(*ast.IdentifierNode).Value
			first, err := eval(n.Arguments[0])
			if err != nil {
				return nil, err
			}
			if name == "fixed" {
				v, err := num(first)
				if err != nil {
					return nil, err
				}
				return new(big.Rat).Mul(v, big.NewRat(1000000, 1)), nil
			}
			s, ok := first.(string)
			if !ok {
				return nil, fmt.Errorf("invalid billing argument")
			}
			if name == "tier" {
				tier = s
				return eval(n.Arguments[1])
			}
			if name == "param" {
				if s != "enable_thinking" || u.ThinkingEnabled == nil {
					return nil, fmt.Errorf("missing_billing_parameter")
				}
				return *u.ThinkingEnabled, nil
			}
			zone, err := time.LoadLocation(s)
			if err != nil {
				return nil, fmt.Errorf("invalid price time zone")
			}
			local := at.In(zone)
			v := local.Hour()
			if name == "minute" {
				v = local.Minute()
			}
			if name == "weekday" {
				v = int(local.Weekday())
			}
			return big.NewRat(int64(v), 1), nil
		}
		return nil, fmt.Errorf("unsupported expression")
	}
	raw, err := eval(e.root)
	if err != nil {
		return Valuation{}, err
	}
	v, err := num(raw)
	if err != nil {
		return Valuation{}, err
	}
	v = new(big.Rat).Quo(v, big.NewRat(1000000, 1))
	if v.Sign() < 0 || v.Cmp(big.NewRat(1000000000, 1)) > 0 {
		return Valuation{}, fmt.Errorf("invalid_amount")
	}
	return Valuation{AmountUSD: v.FloatString(9), Tier: tier}, nil
}
