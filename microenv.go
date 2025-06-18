package microenv

import (
	"reflect"
	"sync"
)

type Awaiter struct {
	mu   sync.Mutex
	done bool
	val  interface{}
	chs  []chan interface{}
}

func newAwaiter() *Awaiter {
	return &Awaiter{chs: make([]chan interface{}, 0)}
}

func (w *Awaiter) addWaiter() <-chan interface{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch := make(chan interface{}, 1)
	if w.done {
		ch <- w.val
		close(ch)
		return ch
	}
	w.chs = append(w.chs, ch)
	return ch
}

func (w *Awaiter) resolve(val interface{}) {
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return
	}
	w.done = true
	w.val = val
	for _, ch := range w.chs {
		ch <- val
		close(ch)
	}
	w.chs = nil
	w.mu.Unlock()
}

type CustomGetFunc func(key string, data *sync.Map, caller string) (interface{}, bool)
type CustomSetFunc func(key string, val interface{}, data *sync.Map, caller string)

type MicroEnv struct {
	data     sync.Map // map[string]interface{}
	awaiters sync.Map // map[string]*Awaiter

	customGet CustomGetFunc
	customSet CustomSetFunc

	face             map[string]*FacePropertyAPI // fixed at startup
	customDescriptor map[string]interface{}

	// NEW: private property support
	privateFlags map[string]bool // key: property name, value: isPrivate
}

type MicroEnvOption func(*MicroEnv)

func WithCustomGet(get CustomGetFunc) MicroEnvOption { return func(m *MicroEnv) { m.customGet = get } }
func WithCustomSet(set CustomSetFunc) MicroEnvOption { return func(m *MicroEnv) { m.customSet = set } }
func WithCustomDescriptor(desc map[string]interface{}) MicroEnvOption {
	return func(m *MicroEnv) {
		m.customDescriptor = desc
	}
}

type FacePropertyAPI struct {
	Get func(caller string) (interface{}, bool)
	Set func(val interface{}, caller string)
}

func NewMicroEnv(data map[string]interface{}, opts ...MicroEnvOption) *MicroEnv {
	m := &MicroEnv{
		face:         make(map[string]*FacePropertyAPI),
		privateFlags: make(map[string]bool), // << NEW
	}
	for k, v := range data {
		m.data.Store(k, v)
	}
	for _, opt := range opts {
		opt(m)
	}
	// Build face map from descriptor and setup private flag map:
	desc := m.Descriptor()
	if children, ok := desc["children"].([]map[string]interface{}); ok {
		for _, entry := range children {
			key := entry["key"].(string)
			// Copy closure
			k := key
			if priv, ok := entry["private"].(bool); ok && priv {
				m.privateFlags[key] = true
			}
			m.face[k] = &FacePropertyAPI{
				Get: func(caller string) (interface{}, bool) {
					val, _, ok := m.Get(k, false, caller)
					return val, ok
				},
				Set: func(val interface{}, caller string) {
					m.Set(k, val, caller)
				},
			}
		}
	}
	return m
}

// Helper: allow access only to descriptor/face keys, with private/caller logic
func (m *MicroEnv) isAllowedKey(key string, caller string) bool {
	if _, exists := m.face[key]; !exists {
		return false
	}
	if !m.privateFlags[key] {
		return true
	}
	return caller == "" // only 'owner' (empty string) can access private
}

func (m *MicroEnv) Get(key string, next bool, caller string) (interface{}, <-chan interface{}, bool) {
	if !m.isAllowedKey(key, caller) {
		return nil, nil, false
	}
	if !next {
		if m.customGet != nil {
			ret, ok := m.customGet(key, &m.data, caller)
			return ret, nil, ok
		}
		val, ok := m.data.Load(key)
		return val, nil, ok
	}
	awRaw, _ := m.awaiters.LoadOrStore(key, newAwaiter())
	aw := awRaw.(*Awaiter)
	return nil, aw.addWaiter(), true
}

func (m *MicroEnv) Set(key string, val interface{}, caller string) {
	if !m.isAllowedKey(key, caller) {
		return
	}
	m.data.Store(key, val)
	if m.customSet != nil {
		m.customSet(key, val, &m.data, caller)
	}
	if aw, ok := m.awaiters.Load(key); ok {
		aw.(*Awaiter).resolve(val)
		m.awaiters.Delete(key)
	}
}

func (m *MicroEnv) Call(key string, payload interface{}, caller string) ([]interface{}, bool) {
	if !m.isAllowedKey(key, caller) {
		return nil, false
	}
	valRaw, ok := m.data.Load(key)
	if !ok {
		return nil, false
	}
	val := reflect.ValueOf(valRaw)
	if val.Kind() != reflect.Func {
		return nil, false
	}
	typ := val.Type()
	if typ.NumIn() != 3 {
		return nil, false
	}
	// Prepare args with caller as string
	args := make([]reflect.Value, 3)
	for i, v := range []interface{}{payload, &m.data, caller} {
		if v == nil {
			args[i] = reflect.Zero(typ.In(i))
		} else {
			args[i] = reflect.ValueOf(v)
		}
	}
	results := val.Call(args)
	res := make([]interface{}, len(results))
	for i := range results {
		res[i] = results[i].Interface()
	}
	return res, true
}

// Returns only initial/descriptor properties.
func (m *MicroEnv) Face() map[string]*FacePropertyAPI {
	result := make(map[string]*FacePropertyAPI, len(m.face))
	for k, f := range m.face {
		result[k] = f
	}
	return result
}

func simpleType(val interface{}) string {
	switch val.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case int, int8, int16, int32, int64,
		float32, float64, uint, uint8, uint16, uint32, uint64:
		return "number"
	}
	t := reflect.TypeOf(val)
	if t == nil {
		return "null"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Float32, reflect.Float64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number"
	case reflect.Func:
		return "function"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Ptr:
		v := reflect.ValueOf(val)
		if v.IsNil() {
			return "null"
		}
		return simpleType(v.Elem().Interface())
	case reflect.Chan:
		return "promise"
	default:
		return "object"
	}
}

func (m *MicroEnv) Descriptor() map[string]interface{} {
	if m.customDescriptor != nil {
		return m.customDescriptor
	}
	children := []map[string]interface{}{}
	m.data.Range(func(k, v interface{}) bool {
		child := map[string]interface{}{
			"key":  k.(string),
			"type": simpleType(v),
		}
		// Add private descriptor if set
		if m.privateFlags != nil && m.privateFlags[k.(string)] {
			child["private"] = true
		}
		children = append(children, child)
		return true
	})
	return map[string]interface{}{
		"children": children,
	}
}
