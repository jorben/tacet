package gui

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

var (
	errorType   = reflect.TypeFor[error]()
	contextType = reflect.TypeFor[context.Context]()
)

// Every exported method of Service becomes a frontend binding, and its arguments and results
// travel as JSON. A non-empty interface, a function or a channel cannot make that trip: the
// binding generator only warns (and CI stays green), the call fails at run time.
func TestEveryServiceMethodCanCrossTheFrontendBridge(t *testing.T) {
	st := reflect.TypeFor[*Service]()
	if st.NumMethod() == 0 {
		t.Fatal("Service has no exported methods")
	}
	for i := range st.NumMethod() {
		m := st.Method(i)
		switch m.Name {
		case "ServiceName", "ServiceStartup", "ServiceShutdown", "ServeHTTP":
			continue // handled by Wails itself, never bound
		}
		ft := m.Type // receiver first
		for j := 1; j < ft.NumIn(); j++ {
			if j == 1 && ft.In(j) == contextType {
				continue // Wails passes the call's context
			}
			if bad := unbindable(ft.In(j), map[reflect.Type]bool{}); bad != "" {
				t.Errorf("Service.%s parameter %d: %s", m.Name, j, bad)
			}
		}
		for j := range ft.NumOut() {
			if ft.Out(j) == errorType && j == ft.NumOut()-1 {
				continue // thrown as an exception
			}
			if bad := unbindable(ft.Out(j), map[reflect.Type]bool{}); bad != "" {
				t.Errorf("Service.%s result %d: %s", m.Name, j, bad)
			}
		}
	}
}

// unbindable names the part of t that encoding/json cannot carry both ways, or "" if none.
func unbindable(t reflect.Type, seen map[reflect.Type]bool) string {
	if seen[t] {
		return ""
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Interface:
		if t.NumMethod() > 0 {
			return t.String() + " is a non-empty interface"
		}
	case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return t.String() + " has no JSON form"
	case reflect.Pointer, reflect.Slice, reflect.Array:
		if bad := unbindable(t.Elem(), seen); bad != "" {
			return t.String() + ": " + bad
		}
	case reflect.Map:
		for _, k := range []reflect.Type{t.Key(), t.Elem()} {
			if bad := unbindable(k, seen); bad != "" {
				return t.String() + ": " + bad
			}
		}
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() || strings.Split(f.Tag.Get("json"), ",")[0] == "-" {
				continue
			}
			if bad := unbindable(f.Type, seen); bad != "" {
				return t.String() + "." + f.Name + ": " + bad
			}
		}
	}
	return ""
}
