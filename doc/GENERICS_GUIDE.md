# Go 泛型在 LazyCache 中的应用指南

> 面向初次使用 Go 泛型的 Gopher，结合 lazycache 项目的实践说明。

---

## 目录

1. [泛型入门：为什么需要泛型？](#一泛型入门为什么需要泛型)
2. [Go 泛型核心语法速查](#二go-泛型核心语法速查)
3. [lazycache 中的泛型设计全解析](#三lazycache-中的泛型设计全解析)
4. [使用 lazycache 的泛型操作指南](#四使用-lazycache-的泛型操作指南)
5. [进阶示例](#五进阶示例)
6. [使用者注意事项（Gotchas）](#六使用者注意事项gotchas)

---

## 一、泛型入门：为什么需要泛型？

### 1.1 没有泛型之前的痛苦

在 Go 1.18 引入泛型之前，如果你想写一个"可以存任意类型值"的缓存，通常有两种方式：

**方式 A：为每种类型写一个专属缓存**

```go
type UserCache struct {
    items map[string]*User
}

type ProductCache struct {
    items map[string]*Product
}
// 每种类型都要重复写一遍，代码爆炸 💥
```

**方式 B：用 `interface{}` / `any` 存储**

```go
type Cache struct {
    items map[string]interface{}
}

func (c *Cache) Get(key string) (interface{}, error) { ... }

// 调用方必须做类型断言，且运行时才知道是否出错：
val, _ := cache.Get("user:1")
user := val.(*User) // 如果实际存的不是 *User，运行时 panic！
```

### 1.2 泛型带来了什么（优势）

泛型（Generics）允许你写**一份代码，适用于多种类型**，且**编译期保证类型安全**：

```go
// 一个缓存，通用于任意类型 V
type Cache[V any] struct {
    items map[string]*item[V]
}

// 使用时，明确告诉编译器 V 是什么
userCache   := New[*User](...)    // V = *User，编译器确保只能存/取 *User
stringCache := New[string](...)   // V = string，编译器确保只能存/取 string
```

**使用泛型的核心优势：**

| 优势 | 说明 |
|---|---|
| **消除类型断言** | 取出的值直接是目标类型，不再需要 `val.(*User)` |
| **编译期类型检查** | 类型不匹配在编译时就报错，而不是运行时 panic |
| **零代码重复** | 一份实现，所有类型共用 |
| **性能更优** | 避免了 `interface{}` 的装箱/拆箱（boxing）开销 |
| **IDE 支持更好** | 编译器知道类型，自动补全和重构更准确 |

---

### 1.3 泛型的劣势与限制（不得不了解）

泛型不是银弹。与 `interface{}` 相比，Go 的泛型存在以下明确的劣势和约束：

#### ❶ 类型在实例化时固定——失去运行时灵活性

`interface{}` 可以在同一个容器中**运行时混入任意类型**，泛型做不到：

```go
// interface{}：一个 map，什么都能放
m := map[string]interface{}{
    "name":  "Alice",    // string
    "age":   30,         // int
    "score": 99.5,       // float64
}

// 泛型：一旦实例化，类型就固定了
cache := New[string](...)
cache.Set("name", "Alice") // ✅ 只能放 string
cache.Set("age", 30)       // ❌ 编译错误：int 不是 string
```

**影响场景：** 如果你的缓存确实需要在同一个实例中存储异构数据（比如一个通用 JSON blob 缓存），`Cache[string]` 或 `Cache[json.RawMessage]` 反而更合适，此时泛型的类型安全变为约束。

#### ❷ 编译产物体积膨胀（Code Bloat）

泛型在编译时会为**每种不同的类型参数**生成独立的代码（Go 采用 *GC Shape Stenciling* 策略）。如果你实例化了十几种不同类型的 Cache，编译后的二进制会比 `interface{}` 版本更大。

```go
// 每种类型参数都会生成对应的方法实现
Cache[*User]     // 生成一套
Cache[*Product]  // 再生成一套
Cache[*Order]    // 再生成一套
// 二进制体积随类型数量线性增长
```

**影响场景：** 对二进制体积敏感的场景（嵌入式、WebAssembly、极致精简的容器镜像）需要注意。

#### ❸ 无法在泛型函数内部做运行时类型分发

`interface{}` 可以结合 `reflect` 和 `type switch` 实现运行时多态，泛型约束为 `any` 时做不到：

```go
// interface{}：可以运行时判断具体类型
func process(v interface{}) {
    switch val := v.(type) {
    case *User:    fmt.Println("user:", val.Name)
    case *Product: fmt.Println("product:", val.SKU)
    case string:   fmt.Println("string:", val)
    }
}

// 泛型：约束为 any 时，无法对 V 做类型开关
func process[V any](v V) {
    switch any(v).(type) {  // 需要先转换为 any 才能断言，绕一圈还是回到 interface{}
    case *User:    ...
    case *Product: ...
    }
}
```

**影响场景：** 序列化框架、ORM、插件系统、通用中间件等需要运行时类型分发的场景，`interface{}` + `reflect` 仍是更自然的选择。

#### ❹ 调用方语法更繁琐

```go
// interface{} 版本：简洁
cache := NewCache()
WithMaxItems(100)

// 泛型版本：调用方必须写类型参数
cache := lazycache.New[*User]("db", loader)
lazycache.WithMaxItems[*User](100)  // 比 WithMaxItems(100) 更繁琐
```

对于不熟悉泛型的团队成员，或者需要频繁实例化的场景，额外的类型标注会增加认知负担和代码噪声。

#### ❺ Go 泛型当前的硬性语言限制

截至 Go 1.21，泛型实现还有一些无法绕过的语言限制：

```go
// 限制 1：方法不能声明新的类型参数
type Cache[V any] struct{}

func (c *Cache[V]) Convert[U any]() U { ... } // ❌ 编译错误！

// 限制 2：不能对泛型类型使用通配符断言
var x any = Cache[int]{}
_, ok := x.(Cache[any]) // ❌ 不被支持

// 限制 3：类型参数不能用于常量表达式
func Make[T any](n int) []T {
    const size = unsafe.Sizeof(T{}) // ❌ 编译错误
}
```

这些限制在某些高级场景下会让泛型难以施展，被迫退回到 `interface{}` + `reflect`。

---

### 1.4 `interface{}` vs 泛型：如何权衡选择？

这是工程决策，没有绝对答案。以下是关键考量维度：

| 考量因素 | 倾向选 `interface{}` | 倾向选泛型 |
|---|---|---|
| **类型多样性** | 同一容器需存储多种不同类型 | 类型固定，每种类型独立实例 |
| **运行时行为** | 需要 `type switch` / `reflect` 动态分发 | 类型在编译期确定，无需运行时判断 |
| **类型安全要求** | 可以接受运行时断言失败的风险 | 要求编译器保证类型一致性 |
| **Go 版本** | 项目仍在 Go 1.17 或更低版本 | 项目已升级到 Go 1.18+ |
| **团队经验** | 团队对泛型不熟悉，学习成本高 | 团队具备泛型基础知识 |
| **二进制体积** | 对产物大小非常敏感 | 对产物大小不敏感 |
| **API 设计** | 是通用框架，需支持未知的未来类型 | 是业务代码，调用方类型已知且固定 |
| **性能要求** | 性能要求宽松 | 需要消除装箱开销，追求极致性能 |
| **代码重用度** | 每种类型的处理逻辑差异大 | 核心逻辑完全相同，只有类型不同 |

**实践建议：**

- **优先用泛型**：当你在写一个库/工具，其核心算法与类型无关（如缓存、排序、集合操作），且 Go 版本 >= 1.18。
- **保留 `interface{}`**：当你在写需要运行时动态分发的框架层代码，或者存储的数据类型在编译期未知。
- **混合使用**：库的内核用泛型保证类型安全，对外 API 的某些扩展点仍可用 `interface{}` 保留灵活性。

---

### 1.5 从 `interface{}` 迁移到泛型：方案与步骤

如果你已有一个使用 `interface{}` 的缓存，想迁移到泛型，推荐以下渐进式路径：

#### 第一步：评估可行性

在开始迁移前，先回答以下问题：

- 这个容器是否被**多个固定类型**使用？（✅ 适合迁移）
- 这个容器是否需要在**运行时动态决定类型**？（❌ 不适合迁移）
- 代码库的 Go 版本是否 >= 1.18？（✅ 必要条件）
- 团队成员是否理解泛型基础？（影响迁移节奏）

#### 第二步：识别所有类型断言点

找出所有对 Get/取值结果进行类型断言的地方——这些正是泛型能消除的痛点：

```bash
# 在项目中搜索类型断言
grep -rn '\.\(\*' ./
grep -rn 'interface{}' ./
grep -rn '\.([A-Z]' ./  # 匹配接口断言
```

假设当前代码是这样的：

```go
// ——迁移前：interface{} 版本——

type OldCache struct {
    mu    sync.RWMutex
    items map[string]interface{}
    ttl   time.Duration
}

func (c *OldCache) Get(key string) (interface{}, error) { ... }
func (c *OldCache) Set(key string, value interface{}) { ... }

// 调用方（分散在各处）：
val, err := oldCache.Get("user:1")
if err != nil { return err }
user := val.(*User) // 危险的类型断言
```

#### 第三步：定义泛型版本（此处即 lazycache）

```go
// ——迁移后：泛型版本——

type Cache[V any] struct {
    mu    sync.RWMutex
    items map[string]*item[V]
    ttl   time.Duration
}

func (c *Cache[V]) Get(ctx context.Context, key string) (V, error) { ... }
func (c *Cache[V]) Set(key string, value V) { ... }

// 调用方：完全类型安全，无断言
user, err := userCache.Get(ctx, "user:1") // user 直接是 *User
```

#### 第四步：按类型分组，逐步替换调用方

**不要一次性全量替换**。按使用类型分组，逐步迁移：

```go
// ——第一阶段：迁移 User 相关——

// 1. 为 User 创建专用 cache 实例（替换原来的 oldCache 对 User 的使用）
userCache := lazycache.New[*User]("db", lazycache.LoaderFunc[*User](
    func(ctx context.Context, key string) (*User, error) {
        return db.QueryUser(ctx, key)
    },
))

// 2. 替换所有 User 相关调用
// 迁移前：
//   val, _ := oldCache.Get("user:1")
//   user := val.(*User)
// 迁移后：
user, err := userCache.Get(ctx, "user:1") // 直接是 *User，无需断言
```

```go
// ——第二阶段：迁移 Product 相关——
productCache := lazycache.New[*Product]("db", productLoader)
product, err := productCache.Get(ctx, "prod:99")

// ——第三阶段：迁移其余类型——
// ...依此类推
```

#### 第五步：处理异构数据的特殊情况

如果原来的 `interface{}` 缓存确实存储了多种不同类型，迁移时有三个选择：

**选择 A：拆分为多个独立 cache（最推荐）**

```go
// 原来一个 cache 混存所有类型 → 拆成多个专用 cache
userCache    := lazycache.New[*User]("db", userLoader)
productCache := lazycache.New[*Product]("db", productLoader)
sessionCache := lazycache.New[*Session]("redis", sessionLoader)

// 优势：完全类型安全，各自配置互不影响
// 劣势：需要管理多个 cache 实例（可以封装到一个 Registry struct 中）
```

**选择 B：使用公共接口作为 V（折中方案）**

```go
// 定义公共接口
type Entity interface {
    EntityType() string
}

// 使用接口作为 V，保留一定灵活性
entityCache := lazycache.New[Entity]("db", entityLoader)

// 取出后仍需断言，但至少 loader 层是类型安全的
entity, _ := entityCache.Get(ctx, "user:1")
if user, ok := entity.(*User); ok {
    fmt.Println(user.Name)
}
```

**选择 C：保持 `Cache[any]`，仅获得装箱优化（最小改动）**

```go
// 几乎等同于原来的 interface{}，改动最小
// 仅获得 lazycache 的懒加载、防击穿等功能，不获得类型安全
anyCache := lazycache.New[any]("db", anyLoader)

val, _ := anyCache.Get(ctx, "user:1")
user := val.(*User) // 仍需类型断言
```

#### 第六步：清理旧代码并运行全量测试

```bash
# 确认旧的 interface{} cache 没有剩余调用方后删除
# 运行全量测试确保迁移正确
go test ./... -race

# 检查是否还有遗留的类型断言
grep -rn '\.\(\*User\)' ./   # 应该没有了
grep -rn '\.\(\*Product\)' ./
```

**迁移总结：**

```
迁移路径：interface{} 缓存
    │
    ├─► 类型固定、追求类型安全    → Cache[具体类型]  ✅ 最推荐
    ├─► 多种类型、有公共行为      → Cache[接口类型]  ⚠️  折中
    ├─► 改动最小、仅需懒加载功能  → Cache[any]       ⚠️  过渡用
    └─► 运行时动态类型分发        → 保持 interface{} ❌  不适合迁移
```

---

## 二、Go 泛型核心语法速查

### 2.1 类型参数（Type Parameters）

类型参数写在方括号 `[...]` 中，紧跟在函数名或类型名之后：

```go
// 泛型函数：T 是类型参数，any 是约束（任意类型均可）
func PrintSlice[T any](s []T) {
    for _, v := range s {
        fmt.Println(v)
    }
}

// 调用时指定类型（或让编译器推断）
PrintSlice[int]([]int{1, 2, 3})
PrintSlice([]string{"a", "b"}) // 编译器自动推断 T = string
```

### 2.2 泛型类型（Generic Types）

Struct、接口等类型也可以有类型参数：

```go
// 泛型 Struct
type Box[T any] struct {
    Value T
}

b := Box[int]{Value: 42}
fmt.Println(b.Value) // 42，类型是 int，无需断言

// 泛型接口
type Repository[T any] interface {
    FindByID(id string) (T, error)
    Save(entity T) error
}
```

### 2.3 类型约束（Type Constraints）

约束限制了类型参数允许的范围：

```go
// any：任意类型（等同于 interface{}）
type Stack[T any] struct { ... }

// comparable：可以用 == 比较的类型
func Contains[T comparable](slice []T, item T) bool { ... }

// 自定义约束（接口形式，联合类型）
type Number interface {
    int | int64 | float64
}

func Sum[T Number](nums []T) T { ... }
```

> **lazycache 使用 `any` 约束**，因为缓存的值可以是任意类型（包括不可比较的类型）。

### 2.4 泛型方法

方法的接收者可以是泛型类型，但**方法本身不能新增额外的类型参数**：

```go
type Cache[V any] struct { ... }

// ✅ 正确：方法可以使用类型的类型参数 V
func (c *Cache[V]) Get(key string) (V, error) { ... }

// ❌ 错误：方法不能添加新的类型参数（Go 当前限制）
func (c *Cache[V]) Transform[U any](key string) (U, error) { ... } // 编译错误！
```

### 2.5 类型推断

编译器在某些情况下可以自动推断类型参数：

```go
// 函数参数中可推断
func Map[T, U any](s []T, f func(T) U) []U { ... }

result := Map([]int{1, 2, 3}, func(x int) string {
    return strconv.Itoa(x)
}) // 编译器推断 T=int, U=string，无需写 Map[int, string]

// 无法从返回值推断（必须显式写）
func Zero[T any]() T { var z T; return z }
z := Zero[int]() // 必须写 [int]，无法推断
```

---

## 三、lazycache 中的泛型设计全解析

lazycache 在整个核心链路上都使用了泛型，下面逐一解析每个泛型构件。

### 3.1 `Cache[V any]`：泛型缓存结构体

```go
// cache.go
type Cache[V any] struct {
    mu            sync.RWMutex
    items         map[string]*item[V]   // 内部 item 也是泛型的
    lru           *lruList[V]           // LRU 链表也是泛型的
    loaders       map[string]Loader[V]  // loader 也是泛型的
    sizeEstimator SizeEstimator[V]      // size 估算函数也是泛型的
    // ...
}
```

**设计要点：** `V any` 使得 `Cache` 可以存储任意类型。整个结构体的所有字段都"传染式"地使用了 `V`，形成了完整的类型安全链。

```go
// 实例化：在 New 调用时确定 V 的具体类型
cache := lazycache.New[*User]("db", myLoader)
// 此后，V 在整个 cache 的生命周期内固定为 *User
```

### 3.2 `Loader[V any]`：泛型接口

```go
// loader.go
type Loader[V any] interface {
    Load(ctx context.Context, key string) (V, error)
}
```

**设计要点：** `Loader` 是一个泛型接口。它要求实现者在加载数据时直接返回 `V` 类型，而不是 `interface{}`。调用方得到的是具体类型，无需任何类型断言。

```go
// 实现 Loader[*User] 接口
type DBUserLoader struct{ db *sql.DB }

func (l *DBUserLoader) Load(ctx context.Context, key string) (*User, error) {
    // 返回值是 *User，完全类型安全
    return queryUserFromDB(l.db, key)
}
```

### 3.3 `LoaderFunc[V any]`：函数类型实现接口

```go
// loader.go
type LoaderFunc[V any] func(ctx context.Context, key string) (V, error)

func (f LoaderFunc[V]) Load(ctx context.Context, key string) (V, error) {
    return f(ctx, key)
}
```

**设计要点：** 这是 Go 中一个经典惯用法——**让函数类型实现接口**。

`LoaderFunc[V]` 是一个泛型函数类型，通过给它定义 `Load` 方法，使它实现了 `Loader[V]` 接口。这样，调用方无需定义一个完整的 struct，可以直接用匿名函数：

```go
// 不需要定义 struct，直接用函数字面量
loader := lazycache.LoaderFunc[*User](func(ctx context.Context, key string) (*User, error) {
    return fetchUserFromDB(ctx, key)
})

cache := lazycache.New[*User]("db", loader)
```

**类比：** 这和标准库中 `http.HandlerFunc` 实现 `http.Handler` 接口的原理完全相同。

### 3.4 `Option[V any]`：泛型函数选项模式

```go
// options.go
type Option[V any] func(*Cache[V])

func WithMaxItems[V any](n int) Option[V] {
    return func(c *Cache[V]) {
        c.maxItems = n
    }
}

func WithTTL[V any](d time.Duration) Option[V] {
    return func(c *Cache[V]) {
        c.ttl = d
    }
}
```

**设计要点：** `Option[V]` 是一个泛型函数类型，它接受 `*Cache[V]` 作为参数。这是 Go 中著名的"Functional Options 模式"与泛型的结合。

函数选项需要与 `Cache[V]` 共享同一个类型参数 `V`，因此 `WithMaxItems` 等函数也必须是泛型的。**这也是调用时必须显式写 `[*User]` 的根本原因**：

```go
// ✅ 正确：显式标注类型参数
lazycache.WithMaxItems[*User](100)
lazycache.WithTTL[*User](5 * time.Minute)

// ❌ 编译错误：编译器无法从 100 这个参数推断出 V 是什么
lazycache.WithMaxItems(100)
```

### 3.5 `SizeEstimator[V any]`：泛型函数类型

```go
// options.go
type SizeEstimator[V any] func(V) int64
```

**设计要点：** `SizeEstimator` 是一个泛型函数类型，接受一个 `V` 类型的值，返回它的字节大小估算。默认实现使用反射：

```go
// cache.go
func defaultSizeEstimator[V any](v V) int64 {
    rv := reflect.ValueOf(v)
    if rv.Kind() == reflect.Ptr && !rv.IsNil() {
        return int64(rv.Elem().Type().Size())  // 指针指向的值的静态大小
    }
    return int64(unsafe.Sizeof(v))             // 非指针类型，直接取 sizeof
}
```

> **注意**：`defaultSizeEstimator` 对指针类型只估算了"直接结构体大小"，不包含动态分配的字段（如 `string`、`[]byte`、嵌套指针等）。对于含大量字符串字段的结构体，建议提供自定义 estimator。

### 3.6 `item[V any]`：泛型内部节点

```go
// item.go
type item[V any] struct {
    key      string
    value    V          // 泛型值字段，编译期确定类型
    expireAt time.Time
    size     int64
    loading  bool
    loadChan chan struct{}
    isNull   bool
    lruPrev  *item[V]  // 链表指针也是泛型的
    lruNext  *item[V]
    // ...
}
```

**设计要点：** `item[V]` 是 `Cache[V]` 的内部节点，`value` 字段的类型是 `V`，完全由外部决定，无需任何类型断言。LRU 双向链表的指针 `lruPrev`/`lruNext` 也使用了 `*item[V]`，保证链表操作的类型一致性。

### 3.7 `lruList[V any]`：泛型数据结构

```go
// lru.go
type lruList[V any] struct {
    mu   sync.Mutex
    head *item[V]
    tail *item[V]
}
```

**设计要点：** LRU 链表本身也是泛型的，这样 `RemoveLast()` 可以直接返回 `*item[V]`，而不是 `interface{}`，避免了类型断言的开销与风险。

### 3.8 泛型工具函数

```go
// cache.go

// zero[V any]() 返回 V 类型的零值
// 等价于 var z V; return z，但写成函数更简洁，可复用
func zero[V any]() V {
    var z V
    return z
}

// safeLoad[V any] 是包级泛型函数，捕获 loader 的 panic
func safeLoad[V any](ctx context.Context, loader Loader[V], key string) (v V, err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("%w: %v", ErrLoaderPanic, r)
        }
    }()
    return loader.Load(ctx, key)
}
```

**设计要点：**

- `zero[V]()` 是一个泛型函数，返回任意类型的零值。在需要返回"空结果"时（如发生错误），直接用 `return zero[V](), err`，比 `var z V; return z, err` 更简洁，且对指针/值类型都正确（不会像 `nil` 只能用于指针）。
- `safeLoad[V]` 演示了**包级泛型函数**的写法——无需在包名上声明类型参数，只需在函数定义时声明即可。

---

## 四、使用 lazycache 的泛型操作指南

### 4.1 实例化：正确指定类型参数

```go
import "github.com/ZebraKK/lazycache"

// ✅ 正确：在 New 时指定 V 的具体类型
userCache    := lazycache.New[*User]("db", userLoader)
productCache := lazycache.New[*Product]("db", productLoader)
stringCache  := lazycache.New[string]("kv", stringLoader)
intCache     := lazycache.New[int64]("counter", counterLoader)
```

**关键规则：** `New` 的类型参数 `[V]` 必须与 loader 的返回类型保持一致：

```go
// ✅ 一致：Cache[*User] 搭配 Loader[*User]
lazycache.New[*User]("db", lazycache.LoaderFunc[*User](func(...) (*User, error) { ... }))

// ❌ 编译错误：类型不匹配
lazycache.New[*User]("db", lazycache.LoaderFunc[*Product](func(...) (*Product, error) { ... }))
```

### 4.2 配置选项：不能省略类型参数

`WithMaxItems`、`WithTTL` 等配置函数都是泛型的，必须显式传入类型参数：

```go
cache := lazycache.New[*User](
    "db",
    <USER_NAME>,
    lazycache.WithMaxItems[*User](10000),    // ✅ 必须写 [*User]
    lazycache.WithMaxBytes[*User](1 << 30),  // ✅ 必须写 [*User]
    lazycache.WithTTL[*User](5*time.Minute), // ✅ 必须写 [*User]
    lazycache.WithSizeEstimator[*User](func(u *User) int64 {
        return int64(len(u.Name) + len(u.Email) + 64)
    }),
)
```

> **为什么不能省略？** 对于 `WithMaxItems[V](n int) Option[V]`，参数 `n` 是 `int` 类型，编译器无法从 `int` 推断出 `V` 应该是 `*User`，因此必须手动指定。

### 4.3 Loader 的两种写法

**方式一：使用 `LoaderFunc`（推荐，轻量级）**

```go
loader := lazycache.LoaderFunc[*User](func(ctx context.Context, key string) (*User, error) {
    return db.QueryUser(ctx, key)
})
```

**方式二：实现 `Loader` 接口（适合有状态的 loader）**

```go
type DBUserLoader struct {
    db      *sql.DB
    metrics *Metrics
}

func (l *DBUserLoader) Load(ctx context.Context, key string) (*User, error) {
    l.metrics.Inc("db_query")
    // ...
    return user, nil
}

loader := &DBUserLoader{db: db, metrics: metrics}
cache := lazycache.New[*User]("db", <USER_NAME>)
```

**选择建议：**

- 简单逻辑、无额外状态 → 用 `LoaderFunc`
- 需要注入依赖（DB 连接、metrics 等）→ 实现完整接口

### 4.4 Get 的返回值处理

```go
user, err := cache.Get(ctx, "user:123", lazycache.WithLoader("db"))

switch {
case err == nil:
    // 正常命中，user 是 *User 类型，无需断言
    fmt.Println(user.Name)

case errors.Is(err, lazycache.ErrNotFound):
    // 该 key 不存在（<USER_NAME> 返回了非瞬时错误，已缓存为 null）
    // user 是 *User 的零值（nil）

case errors.Is(err, lazycache.ErrNoLoader):
    // 未指定 loader 且没有注册过任何 loader

case errors.Is(err, lazycache.ErrUpdateFailed):
    // 发生了瞬时错误（如超时、panic），且没有可用的旧值
}
```

### 4.5 `UpdateConfig` 的类型参数

运行时热更新配置时，同样需要显式类型参数：

```go
cache.UpdateConfig(
    lazycache.WithMaxItems[*User](50000),
    lazycache.WithTTL[*User](10 * time.Minute),
)
```

---

## 五、进阶示例

### 5.1 同时使用多种类型的缓存

```go
// 不同类型的 cache 实例完全独立，互不干扰
userCache := lazycache.New[*User]("db", lazycache.LoaderFunc[*User](
    func(ctx context.Context, key string) (*User, error) {
        return fetchUser(ctx, key)
    },
))

productCache := lazycache.New[*Product]("db", lazycache.LoaderFunc[*Product](
    func(ctx context.Context, key string) (*Product, error) {
        return fetchProduct(ctx, key)
    },
))

configCache := lazycache.New[string]("config", lazycache.LoaderFunc[string](
    func(ctx context.Context, key string) (string, error) {
        return os.Getenv(key), nil
    },
))

// 各自类型安全，无需断言
user, _    := userCache.Get(ctx, "user:1")     // user 是 *User
product, _ := productCache.Get(ctx, "prod:99") // product 是 *Product
val, _     := configCache.Get(ctx, "APP_PORT") // val 是 string
```

### 5.2 为复杂值类型提供精确的 SizeEstimator

`defaultSizeEstimator` 使用反射，估算的是结构体本身的静态大小，**不含字符串、切片等动态分配的内存**。对于内存限制敏感的场景，应提供自定义实现：

```go
type Article struct {
    ID      string
    Title   string
    Content string   // 可能非常大
    Tags    []string
}

cache := lazycache.New[*Article](
    "db",
    articleLoader,
    lazycache.WithSizeEstimator[*Article](func(a *Article) int64 {
        if a == nil {
            return 0
        }
        size := int64(len(a.ID) + len(a.Title) + len(a.Content))
        for _, tag := range a.Tags {
            size += int64(len(tag))
        }
        size += 64 // struct overhead
        return size
    }),
    lazycache.WithMaxBytes[*Article](512 << 20), // 512MB 限制
)
```

### 5.3 工厂函数封装：减少重复的类型标注

当项目中多处需要创建相同配置的缓存时，可以封装工厂函数：

```go
// 封装标准配置，减少每次创建时的重复代码
func newUserCache(name string, loader lazycache.Loader[*User]) *lazycache.Cache[*User] {
    return lazycache.New[*User](
        name,
        <USER_NAME>,
        lazycache.WithMaxItems[*User](50000),
        lazycache.WithMaxBytes[*User](256<<20),
        lazycache.WithTTL[*User](5*time.Minute),
        lazycache.WithSizeEstimator[*User](estimateUserSize),
        lazycache.WithLoaderTimeout[*User](2*time.Second),
    )
}

// 调用处简洁清晰
userCache   := newUserCache("db", dbLoader)
userCacheV2 := newUserCache("api", apiLoader) // 不同 loader，相同配置
```

### 5.4 多 Loader 的注册与切换

```go
cache := lazycache.New[*User]("db", dbLoader)

// 注册备用数据源
cache.RegisterLoader("redis", redisLoader)
cache.RegisterLoader("api", apiLoader)

// 不同场景使用不同 loader
user, _ := cache.Get(ctx, "user:1", lazycache.WithLoader("db"))   // 正常走数据库
user, _ = cache.Get(ctx, "user:1", lazycache.WithLoader("api"))   // 降级走 API

// 注意：所有 loader 的 V 类型必须相同（*User）
// 不能混用 Loader[*User] 和 Loader[*AdminUser]
```

### 5.5 使用接口作为 V 类型

当你确实需要一个缓存存储多种具体类型时，可以用接口作为 `V`：

```go
// 定义公共接口
type Entity interface {
    GetID() string
}

// 使用接口作为 V
cache := lazycache.New[Entity]("db", lazycache.LoaderFunc[Entity](
    func(ctx context.Context, key string) (Entity, error) {
        if strings.HasPrefix(key, "user:") {
            return fetchUser(ctx, key)
        }
        return fetchProduct(ctx, key)
    },
))

// 取出后需要类型断言
entity, _ := cache.Get(ctx, "user:1")
if user, ok := entity.(*User); ok {
    fmt.Println(user.Name)
}
```

> **注意：** 使用接口作为 `V` 会退化回需要类型断言的场景，失去泛型的部分优势。仅在确实需要混合类型时使用，通常更推荐为每种类型建立独立的 cache 实例。

### 5.6 自定义有状态的 Loader

```go
// 带有连接池和重试逻辑的生产级 Loader
type ResilientUserLoader struct {
    primary  *sql.DB
    replica  *sql.DB
    maxRetry int
    logger   *slog.Logger
}

func (l *ResilientUserLoader) Load(ctx context.Context, key string) (*User, error) {
    var lastErr error
    for i := 0; i < l.maxRetry; i++ {
        db := l.primary
        if i > 0 {
            db = l.replica // 重试走只读副本
        }

        user, err := queryUser(ctx, db, key)
        if err == nil {
            return user, nil
        }
        lastErr = err
        l.logger.Warn("load failed, retrying", "attempt", i+1, "err", err)
    }
    return nil, fmt.Errorf("all %d attempts failed: %w", l.maxRetry, lastErr)
}

// 使用
loader := &ResilientUserLoader{
    primary:  primaryDB,
    replica:  replicaDB,
    maxRetry: 3,
    logger:   slog.Default(),
}
cache := lazycache.New[*User]("db", loader)
```

---

## 六、使用者注意事项（Gotchas）

### ⚠️ 6.1 指针类型与零值的混淆

当 `V` 为指针类型（如 `*User`）时，`ErrNotFound` 与零值（`nil`）容易混淆：

```go
user, err := cache.Get(ctx, "user:missing", lazycache.WithLoader("db"))

// 当 err == ErrNotFound 时，user == nil（*User 的零值）
// 不要通过 user == nil 来判断"不存在"，应该判断 err

// ❌ 错误：通过 nil 判断
if user == nil {
    fmt.Println("not found")
}

// ✅ 正确：通过 err 判断
if errors.Is(err, lazycache.ErrNotFound) {
    fmt.Println("not found")
}
if err == nil {
    fmt.Println(user.Name) // 安全使用
}
```

### ⚠️ 6.2 `defaultSizeEstimator` 对指针类型的局限

`defaultSizeEstimator` 对指针类型只计算**被指向结构体的静态大小**，不包含以下内容：

- 字符串字段的实际内容（`string` 的 `Type.Size()` 只有 16 字节，是 header）
- 切片字段的底层数组（`[]byte` 的 `Type.Size()` 只有 24 字节，是 header）
- 嵌套指针指向的数据

```go
type User struct {
    ID      string   // 静态大小 16B，但实际内容可能几十字节
    Profile []byte   // 静态大小 24B，但实际数据可能 KB 级别
}

// defaultSizeEstimator 会严重低估！
// 建议：任何含字符串或切片的结构体都提供自定义 SizeEstimator
cache := lazycache.New[*User](
    "db", loader,
    lazycache.<USER_NAME>[*User](func(u *User) int64 {
        return int64(len(u.ID) + len(u.Profile) + 40)
    }),
)
```

### ⚠️ 6.3 `Cache[V]` 实例不可复制

`Cache[V]` 内部含有 `sync.RWMutex`，**绝对不能复制**：

```go
cache := lazycache.New[*User]("db", loader)

// ❌ 错误：复制含 mutex 的结构体，会导致数据竞争
cache2 := *cache  // 不要这么做！

// ✅ 正确：始终传指针
func useCache(c *lazycache.Cache[*User]) { ... }
useCache(cache)
```

### ⚠️ 6.4 类型参数在实例化后不可更改

`V` 在调用 `New[V]` 时就固定了，整个 cache 实例的生命周期内不可更改：

```go
cache := lazycache.New[*User]("db", loader)

// ❌ 不能：在运行时更改存储类型
// cache.Set("key", &Product{...}) // 编译错误：*Product 不是 *User

// ✅ 正确：需要存不同类型，创建不同实例
userCache    := lazycache.New[*User]("db", userLoader)
productCache := lazycache.New[*Product]("db", productLoader)
```

### ⚠️ 6.5 所有 RegisterLoader 必须返回相同的 V 类型

```go
cache := lazycache.New[*User]("db", dbLoader)

// ✅ 正确：注册的 loader 返回类型与 V 一致
cache.RegisterLoader("api", lazycache.LoaderFunc[*User](apiLoader))

// ❌ 编译错误：类型不匹配
cache.RegisterLoader("api", lazycache.LoaderFunc[*Admin](adminLoader))
```

### ⚠️ 6.6 值类型 vs 指针类型的选择

| | 值类型 `Cache[User]` | 指针类型 `Cache[*User]` |
|---|---|---|
| **零值** | `User{}`（空结构体） | `nil` |
| **每次存取** | 拷贝整个结构体 | 只拷贝指针（8 字节） |
| **适用场景** | 小型不可变值（`int64`、`string`） | 大型结构体 |
| **SizeEstimator** | 估算相对准确 | 默认只估算静态大小，建议自定义 |
| **nil 判断** | 无法为 nil | 零值即 nil，注意与 ErrNotFound 配合 |

**通常推荐：** 较大的结构体使用指针类型，配合自定义 `SizeEstimator`；简单标量类型（`string`、`int64`）使用值类型。

### ⚠️ 6.7 泛型约束为 `any`，不保证 V 的可比较性

`V any` 意味着 `V` 可以是任意类型，包括不可比较的类型（如 `map`、`slice`）。如果在业务代码中需要比较两个 `V` 值，需要自行处理：

```go
// Cache[[]byte] 是合法的，但 []byte 不支持 ==
byteCache := lazycache.New[[]byte]("db", bytesLoader)

val, _ := byteCache.Get(ctx, "key")
// if val == someBytes { ... } // ❌ 编译错误：[]byte 不可比较
if bytes.Equal(val, someBytes) { ... } // ✅ 使用 bytes.Equal
```

---

## 总结

lazycache 的泛型设计体现了以下核心原则：

| 设计决策 | 泛型工具 | 解决的问题 |
|---|---|---|
| `Cache[V any]` | 泛型 struct | 一套代码，类型安全地存储任意类型 |
| `Loader[V any]` | 泛型接口 | 加载器与缓存值类型绑定，无需断言 |
| `LoaderFunc[V]` | 函数类型实现接口 | 轻量级 loader，无需额外 struct |
| `Option[V any]` | 泛型函数类型 | 类型安全的 Functional Options |
| `zero[V]()` | 泛型工具函数 | 通用零值返回，兼容所有类型 |
| `item[V any]` | 泛型内部节点 | 值字段直接是 V 类型，无额外包装 |

### 选择泛型还是 `interface{}`？一句话总结

> **类型固定、追求安全 → 泛型；类型动态、追求灵活 → `interface{}`。**

Go 泛型的最大价值不是"少写几行代码"，而是**在编译期捕获类型错误**。lazycache 通过泛型将类型安全从"使用者的责任"提升为"编译器的保障"——这正是现代 Go 库设计的方向。

---

*本文档基于 lazycache v0.1.x，Go 1.18+。*
