package plush

import (
	"bytes"
	"fmt"
	"html/template"
	"reflect"

	"github.com/gobuffalo/plush/ast"

	"github.com/pkg/errors"
)

type compiler struct {
	ctx      *Context
	program  *ast.Program
	template *Template
}

func (c *compiler) compile() (string, error) {
	bb := &bytes.Buffer{}
	for _, stmt := range c.program.Statements {
		var res interface{}
		var err error
		switch node := stmt.(type) {
		case *ast.ReturnStatement:
			res, err = c.evalReturnStatement(node)
		case *ast.ExpressionStatement:
			var s interface{}
			s, err = c.evalExpression(node.Expression)
			switch s.(type) {
			case ast.Printable, template.HTML:
				res = s
			}
		case *ast.LetStatement:
			res, err = c.evalLetStatement(node)
		}
		if err != nil {
			return "", err
		}

		c.write(bb, res)
	}
	return bb.String(), nil
}

func (c *compiler) write(bb *bytes.Buffer, i interface{}) {
	switch t := i.(type) {
	case string, interfaceable, ast.Printable, bool:
		bb.WriteString(template.HTMLEscaper(t))
	case template.HTML:
		bb.WriteString(string(t))
	case HTMLer:
		bb.WriteString(string(t.HTML()))
	case int64, int, float64:
		bb.WriteString(fmt.Sprint(t))
	case []interface{}:
		for _, ii := range t {
			c.write(bb, ii)
		}
	}
}

func (c *compiler) evalExpression(node ast.Expression) (interface{}, error) {
	switch s := node.(type) {
	case *ast.HTMLLiteral:
		return template.HTML(s.Value), nil
	case *ast.StringLiteral:
		return s.Value, nil
	case *ast.IntegerLiteral:
		return s.Value, nil
	case *ast.FloatLiteral:
		return s.Value, nil
	case *ast.InfixExpression:
		return c.evalInfixExpression(s)
	case *ast.HashLiteral:
		return c.evalHashLiteral(s)
	case *ast.IndexExpression:
		return c.evalIndexExpression(s)
	case *ast.CallExpression:
		return c.evalCallExpression(s)
	case *ast.Identifier:
		return c.evalIdentifier(s)
	case *ast.Boolean:
		return s.Value, nil
	case *ast.ArrayLiteral:
		return c.evalArrayLiteral(s)
	case *ast.ForExpression:
		return c.evalForExpression(s)
	case *ast.IfExpression:
		return c.evalIfExpression(s)
	case *ast.PrefixExpression:
		return c.evalPrefixExpression(s)
	case *ast.FunctionLiteral:
		return c.evalFunctionLiteral(s)
	case nil:
		return nil, nil
	}
	return nil, errors.Errorf("could not evaluate node %T", node)
}

func (c *compiler) evalUserFunction(node *userFunction, args []ast.Expression) (interface{}, error) {
	octx := c.ctx
	defer func() { c.ctx = octx }()
	c.ctx = c.ctx.New()
	for i, p := range node.Parameters {
		a := args[i]
		v, err := c.evalExpression(a)
		if err != nil {
			return nil, err
		}
		c.ctx.Set(p.Value, v)

	}
	return c.evalBlockStatement(node.Block)
}

func (c *compiler) evalFunctionLiteral(node *ast.FunctionLiteral) (interface{}, error) {
	params := node.Parameters
	block := node.Block
	return &userFunction{Parameters: params, Block: block}, nil
}

func (c *compiler) evalPrefixExpression(node *ast.PrefixExpression) (interface{}, error) {
	res, err := c.evalExpression(node.Right)
	if err != nil {
		return nil, err
	}
	switch node.Operator {
	case "!":
		return !c.isTruthy(res), nil
	}
	return nil, errors.Errorf("unknown operator %s", node.Operator)
}

func (c *compiler) evalIfExpression(node *ast.IfExpression) (interface{}, error) {
	// fmt.Println("evalIfExpression")
	con, err := c.evalExpression(node.Condition)
	if err != nil {
		return nil, err
	}

	var r interface{}
	if c.isTruthy(con) {
		r, err = c.evalBlockStatement(node.Block)
	} else {
		if node.ElseBlock != nil {
			r, err = c.evalBlockStatement(node.ElseBlock)
		}
	}

	if err != nil {
		return nil, err
	}

	return r, nil
}

func (c *compiler) isTruthy(i interface{}) bool {
	if i == nil {
		return false
	}
	if b, ok := i.(bool); ok {
		return b
	}
	return true
}

func (c *compiler) evalIndexExpression(node *ast.IndexExpression) (interface{}, error) {
	index, err := c.evalExpression(node.Index)
	if err != nil {
		return nil, err
	}
	left, err := c.evalExpression(node.Left)
	if err != nil {
		return nil, err
	}
	rv := reflect.ValueOf(left)
	switch rv.Kind() {
	case reflect.Map:
		return rv.MapIndex(reflect.ValueOf(index)).Interface(), nil
	case reflect.Array, reflect.Slice:
		if i, ok := index.(int); ok {
			return rv.Index(int(i)).Interface(), nil
		}
	}
	return nil, errors.Errorf("could not index %T with %T", left, index)
}

func (c *compiler) evalHashLiteral(node *ast.HashLiteral) (interface{}, error) {
	m := map[string]interface{}{}
	for ke, ve := range node.Pairs {
		v, err := c.evalExpression(ve)
		if err != nil {
			return nil, err
		}
		m[ke.String()] = v
	}
	return m, nil
}

func (c *compiler) evalLetStatement(node *ast.LetStatement) (interface{}, error) {
	// fmt.Println("evalLetStatement")
	v, err := c.evalExpression(node.Value)
	if err != nil {
		return nil, err
	}
	c.ctx.Set(node.Name.Value, v)
	return nil, nil
}

func (c *compiler) evalIdentifier(node *ast.Identifier) (interface{}, error) {
	if node.Callee != nil {
		c, err := c.evalExpression(node.Callee)
		if err != nil {
			return nil, err
		}
		rv := reflect.ValueOf(c)
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		f := rv.FieldByName(node.Value)
		if !f.IsValid() {
			m := rv.MethodByName(node.Value)
			if !m.IsValid() {
				return nil, errors.Errorf("%+v (%T) is an invalid value", f, f)
			}
			return m.Interface(), nil
		}
		return f.Interface(), nil
	}
	return c.ctx.Value(node.Value), nil
}

func (c *compiler) evalInfixExpression(node *ast.InfixExpression) (interface{}, error) {
	// fmt.Println("evalInfixExpression")
	lres, err := c.evalExpression(node.Left)
	if err != nil {
		return nil, err
	}
	if node.Operator == "&&" {
		if !c.isTruthy(lres) {
			return false, nil
		}
	}
	rres, err := c.evalExpression(node.Right)
	if err != nil {
		return nil, err
	}

	switch node.Operator {
	case "&&", "||":
		return c.boolsOperator(lres, rres, node.Operator)
	}

	switch t := lres.(type) {
	case string:
		return c.stringsOperator(t, rres, node.Operator)
	case int64:
		if r, ok := rres.(int); ok {
			return c.intsOperator(int(t), r, node.Operator)
		}
	case int:
		if r, ok := rres.(int); ok {
			return c.intsOperator(t, r, node.Operator)
		}
	case float64:
		if r, ok := rres.(float64); ok {
			return c.floatsOperator(t, r, node.Operator)
		}
	case bool:
		return c.boolsOperator(lres, rres, node.Operator)
	case nil:
		return nil, nil
	}
	return nil, errors.Errorf("unable to operate (%s) on %T and %T ", node.Operator, lres, rres)
}

func (c *compiler) boolsOperator(l interface{}, r interface{}, op string) (interface{}, error) {
	lt := c.isTruthy(l)
	rt := c.isTruthy(r)
	if op == "||" {
		return lt || rt, nil
	}
	return lt && rt, nil
}

func (c *compiler) intsOperator(l int, r int, op string) (interface{}, error) {
	switch op {
	case "+":
		return l + r, nil
	case "-":
		return l - r, nil
	case "/":
		return l / r, nil
	case "*":
		return l * r, nil
	case "<":
		return l < r, nil
	case ">":
		return l > r, nil
	case "!=":
		return l != r, nil
	case ">=":
		return l >= r, nil
	case "<=":
		return l <= r, nil
	case "==":
		return l == r, nil
	}
	return nil, errors.Errorf("unknown operator for integer %s", op)
}

func (c *compiler) floatsOperator(l float64, r float64, op string) (interface{}, error) {
	switch op {
	case "+":
		return l + r, nil
	case "-":
		return l - r, nil
	case "/":
		return l / r, nil
	case "*":
		return l * r, nil
	case "<":
		return l < r, nil
	case ">":
		return l > r, nil
	case "!=":
		return l != r, nil
	case ">=":
		return l >= r, nil
	case "<=":
		return l <= r, nil
	case "==":
		return l == r, nil
	}
	return nil, errors.Errorf("unknown operator for float %s", op)
}

func (c *compiler) stringsOperator(l string, r interface{}, op string) (interface{}, error) {
	rr := fmt.Sprint(r)
	switch op {
	case "+":
		return l + rr, nil
	// case "-":
	// 	return l - rr, nil
	// case "/":
	// 	return l / rr, nil
	// case "*":
	// 	return l * rr, nil
	case "<":
		return l < rr, nil
	case ">":
		return l > rr, nil
	case "!=":
		return l != rr, nil
	case ">=":
		return l >= rr, nil
	case "<=":
		return l <= rr, nil
	case "==":
		return l == rr, nil
	}
	return nil, errors.Errorf("unknown operator for string %s", op)
}

func (c *compiler) evalCallExpression(node *ast.CallExpression) (interface{}, error) {

	var rv reflect.Value
	if node.Callee != nil {
		c, err := c.evalExpression(node.Callee)
		if err != nil {
			return nil, err
		}
		rc := reflect.ValueOf(c)
		rv = rc.MethodByName(node.Function.String())
	} else {
		f, err := c.evalExpression(node.Function)
		if err != nil {
			return nil, err
		}
		if ff, ok := f.(*userFunction); ok {
			return c.evalUserFunction(ff, node.Arguments)
		}
		rv = reflect.ValueOf(f)
	}
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil, errors.Errorf("%+v (%T) is an invalid function", node.String(), rv)
	}

	args := []reflect.Value{}
	for _, a := range node.Arguments {
		v, err := c.evalExpression(a)
		if err != nil {
			return nil, err
		}
		ar := reflect.ValueOf(v)
		if !ar.IsValid() {
			return nil, errors.Errorf("%+v (%T) is an invalid value", v, v)
		}
		args = append(args, ar)
	}

	rt := rv.Type()
	if rt.NumIn() > 0 {
		last := rt.In(rt.NumIn() - 1)
		if last.Name() == helperContextKind {
			hargs := HelperContext{
				Context:  c.ctx,
				compiler: c,
				block:    node.Block,
			}
			args = append(args, reflect.ValueOf(hargs))
		}
	}

	if len(args) > rt.NumIn() {
		return nil, errors.Errorf("%s too many arguments (%d for %d) - %+v", node.String(), len(args), rt.NumIn(), args)
	}

	res := rv.Call(args)
	if len(res) > 0 {
		if len(res) > 1 {
			if e, ok := res[1].Interface().(error); ok {
				return nil, e
			}
		}
		return res[0].Interface(), nil
	}
	return nil, nil
}

func (c *compiler) evalForExpression(node *ast.ForExpression) (interface{}, error) {
	iter, err := c.evalExpression(node.Iterable)
	if err != nil {
		return nil, err
	}
	riter := reflect.ValueOf(iter)
	ret := []interface{}{}
	switch riter.Kind() {
	case reflect.Map:
		octx := c.ctx
		keys := riter.MapKeys()
		for i := 0; i < len(keys); i++ {
			k := keys[i]
			v := riter.MapIndex(k)
			c.ctx = octx.New()
			c.ctx.Set(node.KeyName, k.Interface())
			c.ctx.Set(node.ValueName, v.Interface())
			res, err := c.evalBlockStatement(node.Block)
			c.ctx = octx
			if err != nil {
				return nil, err
			}
			ret = append(ret, res)
		}
	case reflect.Slice, reflect.Array:
		octx := c.ctx
		for i := 0; i < riter.Len(); i++ {
			c.ctx = octx.New()
			v := riter.Index(i)
			c.ctx.Set(node.KeyName, i)
			c.ctx.Set(node.ValueName, v.Interface())
			res, err := c.evalBlockStatement(node.Block)
			c.ctx = octx
			if err != nil {
				return nil, err
			}
			if res != nil {
				ret = append(ret, res)
			}
		}
	default:
		if it, ok := iter.(Iterator); ok {
			octx := c.ctx
			i := 0
			ii := it.Next()
			for ii != nil {
				c.ctx.Set(node.KeyName, i)
				c.ctx.Set(node.ValueName, ii)
				res, err := c.evalBlockStatement(node.Block)
				c.ctx = octx
				if err != nil {
					return nil, err
				}
				if res != nil {
					ret = append(ret, res)
				}
				ii = it.Next()
				i++
			}
			return ret, nil
		}
		return ret, errors.Errorf("could not iterate over %T", iter)
	}
	return ret, nil
}

func (c *compiler) evalBlockStatement(node *ast.BlockStatement) (interface{}, error) {
	// fmt.Println("evalBlockStatement")
	res := []interface{}{}
	for _, s := range node.Statements {
		i, err := c.evalStatement(s)
		if err != nil {
			return nil, err
		}
		if i != nil {
			res = append(res, i)
		}
	}
	return res, nil
}

func (c *compiler) evalStatement(node ast.Statement) (interface{}, error) {
	// fmt.Println("evalStatement")
	switch t := node.(type) {
	case *ast.ExpressionStatement:
		s, err := c.evalExpression(t.Expression)
		switch s.(type) {
		case ast.Printable, template.HTML:
			return s, err
		}
		return nil, err
	case *ast.ReturnStatement:
		return c.evalReturnStatement(t)
	}
	return nil, errors.Errorf("could not eval statement %T", node)
}

func (c *compiler) evalReturnStatement(node *ast.ReturnStatement) (interface{}, error) {
	// fmt.Println("evalReturnStatement")
	res, err := c.evalExpression(node.ReturnValue)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *compiler) evalArrayLiteral(node *ast.ArrayLiteral) (interface{}, error) {
	res := []interface{}{}
	for _, e := range node.Elements {
		i, err := c.evalExpression(e)
		if err != nil {
			return nil, err
		}
		res = append(res, i)
	}
	return res, nil
}
