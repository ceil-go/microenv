package microenv

import (
	"reflect"
	"sync"
)

type Awaiter struct {
	once sync.Once
	ch   chan interface{}
}

func newAwaiter() *Awaiter {
	return &Awaiter{ch: make(chan interface{}, 1)}
}
func (w *Awaiter) resolve(val interface{}) {
	w.once.Do(func() {
		w.ch <- val
		close(w.ch)
	})
}
func (w *Awaiter) Chan() <-chan interface{} { return w.ch }

type CustomGetFunc func(key string, data map[string]interface{}, caller interface{}) (interface{}, bool)
type CustomSetFunc func(key string, val interface{}, data map[string]interface{}, caller interface{})

type MicroEnv struct {
	data     sync.Map // map[string]interface{}
	awaiters sync.Map // map[string]*Awaiter

	customGet CustomGetFunc
	customSet CustomSetFunc

	faceMu sync.Mutex
	face   map[string]*FaceAPI // lazily init/update on demand

	customDescriptor map[string]interface{}
}

type MicroEnvOption func(*MicroEnv)

func WithCustomGet(get CustomGetFunc) MicroEnvOption { return func(m *MicroEnv) { m.customGet = get } }
func WithCustomSet(set CustomSetFunc) MicroEnvOption { return func(m *MicroEnv) { m.customSet = set } }
func WithCustomDescriptor(desc map[string]interface{}) MicroEnvOption {
	return func(m *MicroEnv) {
		m.customDescriptor = desc
	}
}

func NewMicroEnv(data map[string]interface{}, opts ...MicroEnvOption) *MicroEnv {
	m := &MicroEnv{
		face: make(map[string]*FaceAPI),
	}
	for k, v := range data {
		m.data.Store(k, v)
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *MicroEnv) snapshot() map[string]interface{} {
	tmp := make(map[string]interface{})
	m.data.Range(func(k, v interface{}) bool {
		tmp[k.(string)] = v
		return true
	})
	return tmp
}

func (m *MicroEnv) Get(key string, next bool, caller interface{}) (interface{}, <-chan interface{}, bool) {
	if !next {
		if m.customGet != nil {
			ret, ok := m.customGet(key, m.snapshot(), caller)
			return ret, nil, ok
		}
		val, ok := m.data.Load(key)
		return val, nil, ok
	}
	actual, _ := m.awaiters.LoadOrStore(key, newAwaiter())
	return nil, actual.(*Awaiter).Chan(), true
}

func (m *MicroEnv) Set(key string, val interface{}, caller interface{}) {
	m.data.Store(key, val)
	if m.customSet != nil {
		m.customSet(key, val, m.snapshot(), caller)
	}
	if aw, ok := m.awaiters.Load(key); ok {
		aw.(*Awaiter).resolve(val)
		m.awaiters.Delete(key)
	}
	m.ensureFaceFor(key)
}

// Only supports 3-argument functions!
func (m *MicroEnv) Call(key string, payload interface{}, caller interface{}) ([]interface{}, bool) {
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
		// Only allow functions with signature (payload, data, caller)
		return nil, false
	}
	args := make([]reflect.Value, 3)
	for i, v := range []interface{}{payload, m.snapshot(), caller} {
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

type FaceAPI struct {
	Get func(caller interface{}) (interface{}, bool)
	Set func(val interface{}, caller interface{})
}

func (m *MicroEnv) ensureFaceFor(key string) {
	m.faceMu.Lock()
	defer m.faceMu.Unlock()
	if _, exists := m.face[key]; !exists {
		k := key // capture
		m.face[k] = &FaceAPI{
			Get: func(caller interface{}) (interface{}, bool) {
				val, _, ok := m.Get(k, false, caller)
				return val, ok
			},
			Set: func(val interface{}, caller interface{}) {
				m.Set(k, val, caller)
			},
		}
	}
}

func (m *MicroEnv) ensureFaceForUnlocked(key string) {
	if _, exists := m.face[key]; !exists {
		k := key // closure safety
		m.face[k] = &FaceAPI{
			Get: func(caller interface{}) (interface{}, bool) {
				val, _, ok := m.Get(k, false, caller)
				return val, ok
			},
			Set: func(val interface{}, caller interface{}) {
				m.Set(k, val, caller)
			},
		}
	}
}

func (m *MicroEnv) Face() map[string]*FaceAPI {
	m.faceMu.Lock()
	defer m.faceMu.Unlock()
	m.data.Range(func(k, v interface{}) bool {
		key := k.(string)
		m.ensureFaceForUnlocked(key)
		return true
	})
	result := make(map[string]*FaceAPI, len(m.face))
	for k, f := range m.face {
		result[k] = f
	}
	return result
}

func jsType(val interface{}) string {
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
		return jsType(reflect.Indirect(reflect.ValueOf(val)).Interface())
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
		children = append(children, map[string]interface{}{
			"key":  k.(string),
			"type": jsType(v),
		})
		return true
	})
	return map[string]interface{}{
		"children": children,
	}
}

// --- DEMO

// func main() {
// 	customGet := func(key string, data map[string]interface{}, caller interface{}) (interface{}, bool) {
// 		fmt.Printf("[CUSTOM get] key=%v, caller=%v\n", key, caller)
// 		return data[key], true
// 	}
// 	customSet := func(key string, val interface{}, _ map[string]interface{}, caller interface{}) {
// 		fmt.Printf("[CUSTOM set] key=%v, val=%v, caller=%v\n", key, val, caller)
// 	}
// 	myDescriptor := map[string]interface{}{
// 		"children": []map[string]interface{}{
// 			{"key": "greetingFunction", "type": "function"},
// 			{"key": "additionFunction", "type": "function"},
// 			{"key": "currentCount", "type": "number"},
// 			{"key": "foo", "type": "string"},
// 			{"key": "flexFunc", "type": "function"},
// 		},
// 	}

// 	env := NewMicroEnv(
// 		map[string]interface{}{
// 			"greetingFunction": func(payload interface{}, data map[string]interface{}, caller interface{}) string {
// 				name, _ := payload.(string)
// 				return "Hello, " + name + "!"
// 			},
// 			"additionFunction": func(payload interface{}, data map[string]interface{}, caller interface{}) int {
// 				arr, _ := payload.([]int)
// 				if len(arr) == 2 {
// 					return arr[0] + arr[1]
// 				}
// 				return 0
// 			},
// 			"currentCount": 0,
// 			"foo":          "bar",
// 		},
// 		WithCustomGet(customGet),
// 		WithCustomSet(customSet),
// 		WithCustomDescriptor(myDescriptor),
// 	)

// 	env.Set("foo", 10, nil)
// 	env.Set("foo", "not bar", "admin")
// 	val1, _, _ := env.Get("foo", false, nil)
// 	fmt.Println("foo (anonymous get):", val1)
// 	val2, _, _ := env.Get("foo", false, "reader")
// 	fmt.Println("foo (reader):", val2)

// 	_, ch, _ := env.Get("foo", true, "waiter")
// 	go func() {
// 		time.Sleep(2000 * time.Millisecond)
// 		env.Set("foo", "42", "updater")
// 	}()
// 	fmt.Println("awaited foo update:", <-ch)

// 	face := env.Face()
// 	face["currentCount"].Set(123, "counter")
// 	cc, ok := face["currentCount"].Get(nil)
// 	fmt.Println("currentCount (Face, anonymous):", cc, ok)
// 	face["currentCount"].Set(200, nil)
// 	cc, _ = face["currentCount"].Get("userX")
// 	fmt.Println("currentCount (Face, userX):", cc)

// 	// Face usage:
// 	nameFun, _ := face["greetingFunction"].Get(nil)
// 	fmt.Println("Face greet:", nameFun.(func(interface{}, map[string]interface{}, interface{}) string)("FaceUser", env.snapshot(), nil))

// 	addFun, _ := face["additionFunction"].Get("adder")
// 	fmt.Println("Face add:", addFun.(func(interface{}, map[string]interface{}, interface{}) int)([]int{10, 5}, env.snapshot(), "adder"))

// 	env.Set("flexFunc", func(payload interface{}, data map[string]interface{}, caller interface{}) string {
// 		return fmt.Sprintf("[FLEXFUNC CALLED] caller=%v data[foo]=%v payload=%#v", caller, data["foo"], payload)
// 	}, nil)
// 	if r, ok := env.Call("flexFunc", map[string]int{"hello": 123}, "DEMOUSER"); ok {
// 		fmt.Println("MicroEnv.Call 'flexFunc' result:", r[0])
// 	}

// 	r, ok := env.Call("greetingFunction", "Zeta", "callerguy")
// 	if ok {
// 		fmt.Println("MicroEnv.Call greetingFunction:", r[0])
// 	}

// 	r, ok = env.Call("additionFunction", []int{7, 11}, nil)
// 	if ok {
// 		fmt.Println("MicroEnv.Call additionFunction:", r[0])
// 	}

// 	fmt.Println("Descriptor:", env.Descriptor())
// }
