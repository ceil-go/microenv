package microenv

import (
	"sync"
	"testing"
	"time"
)

func TestMicroEnvBasicSetGet(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"x": 123,
		"y": "abc",
	})

	// Only keys in the descriptor/face are accessible
	val, ch, ok := env.Get("x", false, "")
	if !ok || ch != nil || val != 123 {
		t.Fatalf("unexpected value for 'x': %v", val)
	}
	val, _, ok = env.Get("y", false, "")
	if !ok || val != "abc" {
		t.Fatalf("unexpected value for 'y': %v", val)
	}
	_, _, ok = env.Get("missing", false, "")
	if ok {
		t.Fatal("should not find non-existing key")
	}

	// Set and Get
	env.Set("x", 42, "")
	val, _, ok = env.Get("x", false, "")
	if !ok || val != 42 {
		t.Fatalf("set/get failed: %v", val)
	}
}

func TestMicroEnvAwaiterNext(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"x": 1,
	})
	ch := make(chan interface{}, 1)
	go func() {
		_, next, ok := env.Get("x", true, "")
		if !ok {
			t.Error("expected ok for awaiter")
		}
		ch <- (<-next)
	}()
	time.Sleep(50 * time.Millisecond)
	env.Set("x", 99, "")
	select {
	case res := <-ch:
		if res != 99 {
			t.Errorf("expect awaited value (99), got %v", res)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("Timed out waiting for awaiter")
	}
}

func TestMicroEnvCustomGetSet(t *testing.T) {
	getCalled, setCalled := false, false
	env := NewMicroEnv(
		map[string]interface{}{"x": 0},
		WithCustomGet(func(key string, data *sync.Map, caller string) (interface{}, bool) {
			getCalled = true
			return 555, true
		}),
		WithCustomSet(func(key string, val interface{}, data *sync.Map, caller string) {
			setCalled = true
		}),
	)
	val, _, ok := env.Get("x", false, "")
	if !ok || val != 555 || !getCalled {
		t.Errorf("custom get was not called")
	}
	env.Set("x", 1234, "")
	if !setCalled {
		t.Errorf("custom set was not called")
	}
}

func TestMicroEnvCall(t *testing.T) {
	sumFunc := func(payload interface{}, data *sync.Map, caller interface{}) int {
		vals := payload.([]int)
		return vals[0] + vals[1]
	}
	env := NewMicroEnv(map[string]interface{}{
		"sum": sumFunc,
	})

	// Call with allowed key
	res, ok := env.Call("sum", []int{2, 3}, "")
	if !ok || len(res) != 1 || res[0] != 5 {
		t.Fatalf("Call failed: got %v %v", res, ok)
	}

	// Call a missing key
	_, ok = env.Call("notexist", nil, "")
	if ok {
		t.Error("should not call on missing key")
	}

	// Call with a non-func
	env = NewMicroEnv(map[string]interface{}{
		"foo": 123,
	})
	_, ok = env.Call("foo", nil, "")
	if ok {
		t.Error("should not call on non-function value")
	}
}

func TestMicroEnvDescriptorAndFace(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{"a": 1, "b": true})
	face := env.Face()
	desc := env.Descriptor()
	children, _ := desc["children"].([]map[string]interface{})
	wantKeys := map[string]bool{"a": false, "b": false}
	for _, entry := range children {
		k := entry["key"].(string)
		wantKeys[k] = true
	}
	for k, ok := range wantKeys {
		if !ok {
			t.Errorf("missing key in descriptor: %v", k)
		}
	}
	// Face map should have same keys
	for k := range wantKeys {
		if _, ok := face[k]; !ok {
			t.Errorf("missing key in face: %v", k)
		}
	}
}

func TestCustomDescriptor(t *testing.T) {
	cdesc := map[string]interface{}{
		"children": []map[string]interface{}{
			{"key": "magic", "type": "number"},
		},
	}
	env := NewMicroEnv(map[string]interface{}{
		"magic": 42,
		"skip":  "nope",
	}, WithCustomDescriptor(cdesc))
	// Only "magic" present
	_, _, ok := env.Get("magic", false, "")
	if !ok {
		t.Error("expected 'magic' key to be allowed")
	}
	_, _, ok = env.Get("skip", false, "")
	if ok {
		t.Error("should not allow keys outside custom descriptor")
	}
}

func TestSimpleTypeCovers(t *testing.T) {
	cases := []struct {
		input interface{}
		want  string
	}{
		{nil, "null"},
		{true, "boolean"},
		{"hi", "string"},
		{42, "number"},
		{3.14, "number"},
		{[]int{1, 2}, "array"},
		{map[string]int{"a": 1}, "object"},
		{func() {}, "function"},
		{make(chan int), "promise"},
	}
	for _, tc := range cases {
		tpe := simpleType(tc.input)
		if tpe != tc.want {
			t.Errorf("simpleType(%v) = %q, want %q", tc.input, tpe, tc.want)
		}
	}
	// Cover non-nil struct pointer
	type testStruct struct{}
	s := testStruct{}
	sp := &s
	if simpleType(sp) != "object" {
		t.Errorf("simpleType(non-nil ptr) wrong: got %s", simpleType(sp))
	}

	// Cover nil struct pointer
	var snil *testStruct = nil
	if simpleType(snil) != "null" {
		t.Errorf("simpleType(nil ptr) wrong: got %s", simpleType(snil))
	}
}

func TestFacePropertyAPI(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"a": 1,
	})
	face := env.Face()
	getter := face["a"].Get
	setter := face["a"].Set

	setter(10, "")
	val, ok := getter("")
	if !ok || val != 10 {
		t.Fatalf("FacePropertyAPI failed: %v", val)
	}
}

func TestMicroEnv_ConcurrentSetGet(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{"key": 0})
	const n = 10
	done := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			for j := 0; j < 10; j++ {
				env.Set("key", idx*10+j, "")
			}
			val, _, ok := env.Get("key", false, "")
			if !ok {
				t.Errorf("Concurrent get failed")
			}
			_ = val
			done <- true
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

func TestMicroEnvMultiAwaiters(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{"foo": 1})
	n := 5
	chs := make([]<-chan interface{}, n)
	for i := 0; i < n; i++ {
		_, ch, ok := env.Get("foo", true, "")
		if !ok {
			t.Fatal("Could not await on key")
		}
		chs[i] = ch
	}
	env.Set("foo", 42, "")
	for i := 0; i < n; i++ {
		val := <-chs[i]
		if val != 42 {
			t.Fatalf("Awaiter did not receive new value")
		}
	}
}

func TestAwaiterGetAfterResolved(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{"foo": 10})
	env.Set("foo", 5, "")
	_, ch, _ := env.Get("foo", true, "")

	env.Set("foo", 6, "")
	if <-ch != 6 {
		t.Error("Should get new value, not old")
	}
}
