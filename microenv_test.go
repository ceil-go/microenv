package microenv

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// Helper for capturing test caller context
type testCaller string

func TestMicroEnv_SetAndGet(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{"a": 123, "b": "test"})
	env.Set("a", 456, nil)
	val, _, ok := env.Get("a", false, nil)
	if !ok || val != 456 {
		t.Errorf("Expected 456 for 'a'; got %v (ok=%v)", val, ok)
	}
	// Test Get of non-existent key
	_, _, ok = env.Get("nope", false, nil)
	if ok {
		t.Error("Expected not found for 'nope'")
	}
}

func TestMicroEnv_CustomGetSet(t *testing.T) {
	getCalled, setCalled := false, false
	customGet := func(key string, env *MicroEnv, caller interface{}) (interface{}, bool) {
		getCalled = true
		return "CUSTOM-" + key, true
	}
	customSet := func(key string, val interface{}, env *MicroEnv, caller interface{}) {
		setCalled = true
	}
	env := NewMicroEnv(map[string]interface{}{}, WithCustomGet(customGet), WithCustomSet(customSet))
	env.Set("x", 99, testCaller("who"))
	if !setCalled {
		t.Error("customSet not called")
	}
	val, _, _ := env.Get("z", false, testCaller("who2"))
	if val != "CUSTOM-z" || !getCalled {
		t.Errorf("customGet result or invocation error; got %v ", val)
	}
}

func TestMicroEnv_Awaiters(t *testing.T) {
	env := NewMicroEnv(nil)
	key := "foo"
	got := make(chan interface{}, 1)
	_, ch, ok := env.Get(key, true, nil)
	if !ok || ch == nil {
		t.Fatal("Expected awaiter channel")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		env.Set(key, 999, nil)
	}()
	select {
	case v := <-ch:
		got <- v
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for awaiter")
	}
	if val := <-got; val != 999 {
		t.Errorf("Expected 999, got %v", val)
	}
}

func TestMicroEnv_Call(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"sumfn": func(payload interface{}, env *MicroEnv, caller interface{}) int {
			arr := payload.([]int)
			return arr[0] + arr[1]
		},
		"strfn": func(payload interface{}, env *MicroEnv, caller interface{}) string {
			return "hi:" + payload.(string)
		},
		"badfn": func(a int) {},
	})
	res, ok := env.Call("sumfn", []int{2, 3}, nil)
	if !ok || res[0] != 5 {
		t.Errorf("sumfn call failed")
	}
	res, ok = env.Call("strfn", "q", nil)
	if !ok || res[0] != "hi:q" {
		t.Errorf("strfn call failed")
	}
	_, ok = env.Call("badfn", nil, nil)
	if ok {
		t.Error("badfn should not be callable")
	}
	_, ok = env.Call("nope", nil, nil)
	if ok {
		t.Error("nope should not be found")
	}
}

func TestMicroEnv_FaceAPI(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"x": 111,
		"y": 222,
	})
	face := env.Face()
	if len(face) != 2 {
		t.Errorf("Expected 2 face fields, got %d", len(face))
	}
	face["x"].Set(456, "userA")
	val, ok := face["x"].Get(nil)
	if !ok || val != 456 {
		t.Errorf("Face Set/Get failed for x")
	}
	face["y"].Set("test", nil)
	val, ok = face["y"].Get(nil)
	if val != "test" || !ok {
		t.Errorf("Face Set/Get failed for y")
	}
}

func TestMicroEnv_FaceForFunction(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"f": func(payload interface{}, env *MicroEnv, caller interface{}) string {
			return payload.(string) + "-xx"
		},
	})
	fn, ok := env.Face()["f"].Get(nil)
	if !ok {
		t.Error("Face Get for function failed")
	}
	out := fn.(func(interface{}, *MicroEnv, interface{}) string)("abc", env, nil)
	if out != "abc-xx" {
		t.Errorf("function call via Face wrong output: %v", out)
	}
}

func TestMicroEnv_Descriptor(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"a": 1,
		"b": "x",
		"c": true,
		"d": []int{1, 2},
		"f": func(interface{}, *MicroEnv, interface{}) string { return "" },
	})
	desc := env.Descriptor()
	children, ok := desc["children"].([]map[string]interface{})
	if !ok || len(children) != 5 {
		t.Fatalf("Descriptor children wrong (got %v)", children)
	}
	typeMap := map[string]string{}
	for _, child := range children {
		typeMap[child["key"].(string)] = child["type"].(string)
	}
	if typeMap["a"] != "number" || typeMap["b"] != "string" || typeMap["c"] != "boolean" {
		t.Error("Type detection error")
	}
	if typeMap["f"] != "function" {
		t.Error("Function not recognized in descriptor")
	}
	if typeMap["d"] != "array" {
		t.Error("Slice not recognized in descriptor")
	}
}

func TestSimpleType_AllCases(t *testing.T) {
	type customstruct struct{}
	tests := []struct {
		val  interface{}
		want string
	}{
		{nil, "null"},
		{1, "number"},
		{1.1, "number"},
		{uint(1), "number"},
		{"abc", "string"},
		{true, "boolean"},
		{func() {}, "function"},
		{[]int{1}, "array"},
		{map[string]int{}, "object"},
		{&customstruct{}, "object"},
		{make(chan int), "promise"},
	}
	for _, tt := range tests {
		got := simpleType(tt.val)
		if got != tt.want {
			t.Errorf("simpleType(%v) = %q, want %q", reflect.TypeOf(tt.val), got, tt.want)
		}
	}
}

func TestMicroEnv_CustomDescriptor(t *testing.T) {
	desc := map[string]interface{}{
		"children": []map[string]interface{}{
			{"key": "a", "type": "alpha"},
		},
	}
	env := NewMicroEnv(nil, WithCustomDescriptor(desc))
	got := env.Descriptor()
	if !reflect.DeepEqual(got, desc) {
		t.Error("Custom descriptor not returned")
	}
}

// Concurrency: Simultaneous Set and Get on the same key
func TestMicroEnv_ConcurrentSetGet_SameKey(t *testing.T) {
	env := NewMicroEnv(nil)
	const n = 50
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k"
			env.Set(key, i, nil)
			val, _, ok := env.Get(key, false, nil)
			if !ok {
				t.Errorf("Concurrent Set/Get failed: expected ok==true after Set, got false")
			}
			// Do not assert value match because multiple goroutines racing; just checking no panic
			_ = val
		}(i)
	}
	wg.Wait()
}

// Multiple Awaiters for same key all receive the update
func TestMicroEnv_MultipleAwaiters(t *testing.T) {
	env := NewMicroEnv(nil)
	key := "waitkey"
	const waiters = 10
	chans := make([]<-chan interface{}, waiters)
	for i := range chans {
		_, ch, ok := env.Get(key, true, nil)
		if !ok || ch == nil {
			t.Fatalf("Failed to create awaiter #%d", i)
		}
		chans[i] = ch
	}

	env.Set(key, "fired!", nil)

	for i, ch := range chans {
		select {
		case v := <-ch:
			if v != "fired!" {
				t.Errorf("Awaiter %d got %v, want 'fired!'", i, v)
			}
		case <-time.After(time.Second):
			t.Errorf("Awaiter %d timeout", i)
		}
	}
}

// Awaiter is cleaned up after being resolved
func TestMicroEnv_AwaiterCleanup(t *testing.T) {
	env := NewMicroEnv(nil)
	key := "cleanme"
	_, ch, _ := env.Get(key, true, nil)
	env.Set(key, 42, nil)
	<-ch
	// Should be deleted from map
	if _, ok := env.awaiters.Load(key); ok {
		t.Error("Awaiter not cleaned up after resolve and Set")
	}
}

// Function signature mismatch in Call and Face
func TestMicroEnv_Call_FuncSignatureMismatch(t *testing.T) {
	env := NewMicroEnv(map[string]interface{}{
		"badfn": func(x int) int { return x },
	})
	// This function only takes 1 arg; should fail Call
	_, ok := env.Call("badfn", 5, nil)
	if ok {
		t.Error("Call should fail for function with wrong signature")
	}

	// Also test that Face returns (value, false) for non-three-arg function
	env.Set("goodfn", func(a, b, c interface{}) int { return 7 }, nil)
	face := env.Face()
	val, ok := face["badfn"].Get(nil)
	if !ok {
		// It's OK for Get to succeed, but type will be bad.
		_, isFunc := val.(func(interface{}, *MicroEnv, interface{}) int)
		if isFunc {
			t.Error("badfn should not be convertible to correct func signature")
		}
	}
}

// Await after value was already set (shouldn't fire again)
func TestMicroEnv_AwaitAfterSetShouldNotFire(t *testing.T) {
	env := NewMicroEnv(nil)
	key := "afterset"
	env.Set(key, 7, nil)
	_, ch, ok := env.Get(key, true, nil)
	if !ok || ch == nil {
		t.Fatal("Get with next=true failed")
	}
	select {
	case v := <-ch:
		// Should NOT fire, since Set happened before Get-next
		t.Errorf("Unexpected value after set: %v", v)
	case <-time.After(100 * time.Millisecond):
		// Pass: channel should never deliver a value
	}
}
