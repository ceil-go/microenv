# microenv

A small Go package for managing key-value environments with some extras.

## What is this?

- Thread-safe key/value “environment”
- Await support (wait for a value to change)
- “Private” keys for limited access
- Custom getter/setter hooks
- Function-valued properties (“Call”)
- Face API (“object view” for supported keys)

## Example

```go
package main

import (
    "fmt"
    "sync"
    "github.com/ceil-go/microenv"
)

func main() {
    // Custom getter and setter
    customGet := func(key string, data *sync.Map, caller string) (interface{}, bool) {
        if key == "secret" && caller != "admin" {
            return nil, false
        }
        val, ok := data.Load(key)
        return val, ok
    }
    customSet := func(key string, val interface{}, data *sync.Map, caller string) {
        fmt.Printf("Custom set: %s = %v (by %s)\n", key, val, caller)
        data.Store(key, val)
    }

    // Private flag can be given via custom descriptor
    descriptor := map[string]interface{}{
        "children": []map[string]interface{}{
            {"key": "public", "type": "string"},
            {"key": "secret", "type": "string", "private": true},
            {"key": "sum", "type": "function"},
        },
    }

    env := microenv.NewMicroEnv(
        map[string]interface{}{
            "public": "hello",
            "secret": "shh",
            "sum": func(payload interface{}, data *sync.Map, caller string) int {
                vals := payload.([]int)
                return vals[0] + vals[1]
            },
        },
        microenv.WithCustomGet(customGet),
        microenv.WithCustomSet(customSet),
        microenv.WithCustomDescriptor(descriptor),
    )

    // Get/Set normal property
    val, _, ok := env.Get("public", false, "")
    fmt.Println("public:", val, ok)
    env.Set("public", "world", "")

    // Private property (only caller "" can access)
    _, _, ok = env.Get("secret", false, "admin") // ok
    _, _, ok2 := env.Get("secret", false, "user") // not ok
    fmt.Println("admin access:", ok, "user access:", ok2)

    // Await for a value change
    _, ch, _ := env.Get("public", true, "")
    go func() {
        env.Set("public", "updated", "") // triggers notification
    }()
    updated := <-ch
    fmt.Println("Got notified of update:", updated)

    // Call function property
    result, _ := env.Call("sum", []int{3, 7}, "")
    fmt.Println("sum(3,7):", result[0])

    // Use the face API to hide internals and only allow predeclared properties
    face := env.Face()
    face["public"].Set("demo", "")
    val, _ = face["public"].Get("")
    fmt.Println("face, public key:", val)
}
```

## Features Recap

- Basic set/get (thread-safe)
- Await value update with a channel
- Mark properties private (only “owner” can access)
- Custom hook logic for get/set
- Call function values directly by key
- Show only allowed keys via “Face” and descriptor

## Tests

Run tests with:

```sh
go test
```

## License

MIT License.  
See [LICENSE](LICENSE) file for details.
