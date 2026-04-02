# LazyCache

**[English](README.md) | 中文**

一个高性能的 Go 语言懒加载缓存中间件，支持 LRU 淘汰策略、防缓存击穿和零阻塞查询。

## 特性

- 🚀 **零阻塞查询** — 缓存过期时立即返回旧值，后台异步刷新
- 🛡️ **防缓存击穿** — 单 Key 级别的加载去重，防止大量并发请求同时穿透到数据源
- 🔒 **防缓存穿透** — 空值缓存，避免对不存在的 Key 重复查询后端
- ⚡ **高性能** — Get 操作 1600 万+ ops/sec，延迟低于 100ns
- 🔧 **热更新配置** — 运行时动态调整缓存大小、TTL 等参数
- 📊 **智能淘汰** — LRU 策略，同时支持条目数量和字节大小双重限制
- 🎯 **类型安全** — 基于 Go 泛型实现，编译期类型检查
- 🔌 **多数据源** — 支持注册和切换多个数据加载器
- 📈 **内置监控指标** — 追踪命中、未命中、淘汰及刷新性能

## 安装

```bash
go get github.com/ZebraKK/lazycache
```

**环境要求**：Go 1.18+（使用了泛型）

## 快速开始

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/ZebraKK/lazycache"
)

type User struct {
    ID   string
    Name string
}

func main() {
    // 创建缓存并配置参数
    cache := lazycache.New[*User](
        lazycache.WithMaxItems[*User](10000),
        lazycache.WithMaxBytes[*User](1<<30), // 1GB
        lazycache.WithTTL[*User](5*time.Minute),
    )

    // 注册数据加载器
    cache.RegisterLoader("db", lazycache.LoaderFunc[*User](
        func(ctx context.Context, key string) (*User, error) {
            // 从数据库获取数据
            return &User{ID: key, Name: "Alice"}, nil
        },
    ))

    ctx := context.Background()

    // 使用懒加载获取（默认异步模式）
    // 缓存过期时立即返回旧值，后台触发刷新
    user, err := cache.Get(ctx, "user:123", lazycache.WithLoader("db"))
    if err != nil {
        panic(err)
    }
    fmt.Printf("User: %+v\n", user)

    // 使用同步模式获取
    // 等待刷新完成后再返回
    user, err = cache.Get(ctx, "user:456",
        lazycache.WithLoader("db"),
        lazycache.WithSync(),
    )

    // 手动写入
    cache.Set("user:789", &User{ID: "789", Name: "Bob"})

    // 使缓存失效
    cache.Invalidate("user:123")

    // 获取统计信息
    stats := cache.Stats()
    fmt.Printf("命中率: %.2f%%\n", stats.HitRate*100)
    fmt.Printf("命中: %d，未命中: %d\n", stats.Hits, stats.Misses)
}
```

## 核心概念

### 懒加载（异步模式）

LazyCache 的核心特性是**零阻塞懒加载**：

```go
// 缓存过期时的行为：
// 1. 立即返回旧值（不阻塞！）
// 2. 触发后台刷新
// 3. 下次请求获得新数据
user, _ := cache.Get(ctx, "key", lazycache.WithLoader("db"))
```

优势：
- ✅ 稳定的低延迟（P99 < 1ms）
- ✅ 缓存刷新时用户无感知阻塞
- ✅ 对慢速后端的优雅降级

### 防缓存击穿

当多个 goroutine 同时请求同一个未缓存的 Key 时：

```go
// 只有一个 goroutine 会执行数据加载
// 其余 goroutine 等待并共享加载结果
var wg sync.WaitGroup
for i := 0; i < 100; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        // 100 个 goroutine，只产生 1 次数据库查询
        cache.Get(ctx, "key", lazycache.WithLoader("db"), lazycache.WithSync())
    }()
}
wg.Wait()
```

### 空值缓存（防缓存穿透）

防止缓存穿透攻击：

```go
cache.RegisterLoader("db", lazycache.LoaderFunc[*User](
    func(ctx context.Context, key string) (*User, error) {
        // Key 不存在
        return nil, errors.New("not found")
    },
))

// 第一次调用：查询数据库，缓存错误结果
_, err1 := cache.Get(ctx, "missing", lazycache.WithLoader("db"), lazycache.WithSync())

// 第二次调用：返回已缓存的错误，不再查询数据库
_, err2 := cache.Get(ctx, "missing", lazycache.WithLoader("db"), lazycache.WithSync())
```

### 双重内存限制

缓存同时遵守两种限制：

```go
cache := lazycache.New[string](
    lazycache.WithMaxItems[string](10000),  // 最多 10000 条
    lazycache.WithMaxBytes[string](1<<30),  // 最多 1GB 内存
)
// 任一限制触发时，LRU 淘汰策略生效
```

## API 参考

### 创建缓存

```go
func New[V any](opts ...Option[V]) *Cache[V]
```

**配置选项：**
- `WithMaxItems[V](n int)` — 最大缓存条目数（默认：10000）
- `WithMaxBytes[V](bytes int64)` — 最大总字节数（默认：1GB）
- `WithTTL[V](duration time.Duration)` — 默认 TTL（默认：5 分钟）
- `WithSizeEstimator[V](fn SizeEstimator[V])` — 自定义大小计算函数

### 缓存操作

#### Get

```go
func (c *Cache[V]) Get(ctx context.Context, key string, opts ...GetOption) (V, error)
```

**选项：**
- `WithLoader(name string)` — 指定使用哪个加载器
- `WithSync()` — 同步模式（等待加载/刷新完成）
- `WithAsync()` — 异步模式（返回旧值，后台刷新）*默认*
- `WithTTLOverride(duration time.Duration)` — 本次 Get 覆盖默认 TTL

**返回值：**
- 来自缓存或数据源加载的值
- 加载器返回错误时返回 `ErrNotFound`（空值已缓存）
- 未指定加载器时返回 `ErrNoLoader`

#### Set

```go
func (c *Cache[V]) Set(key string, value V, opts ...SetOption)
```

手动写入缓存值。

**选项：**
- `WithSetTTL(duration time.Duration)` — 本条目自定义 TTL
- `WithSetSize(bytes int64)` — 手动指定条目大小

#### Invalidate

```go
func (c *Cache[V]) Invalidate(key string)
```

从缓存中移除指定 Key。

#### RegisterLoader

```go
func (c *Cache[V]) RegisterLoader(name string, loader Loader[V])
```

注册数据源。

**Loader 接口：**
```go
type Loader[V any] interface {
    Load(ctx context.Context, key string) (V, error)
}
```

**快捷方式：**
```go
lazycache.LoaderFunc[V](func(ctx context.Context, key string) (V, error) {
    // 你的加载逻辑
})
```

#### UpdateConfig

```go
func (c *Cache[V]) UpdateConfig(opts ...Option[V])
```

运行时热更新配置。

#### Stats

```go
func (c *Cache[V]) Stats() Snapshot
```

返回缓存统计信息：
```go
type Snapshot struct {
    Hits           int64   // 命中次数
    Misses         int64   // 未命中次数
    Evictions      int64   // 淘汰次数
    RefreshSuccess int64   // 后台刷新成功次数
    RefreshFail    int64   // 后台刷新失败次数
    HitRate        float64 // 命中率
}
```

### 进阶用法

#### 多数据源

```go
cache := lazycache.New[*User]()

// 注册多个加载器
cache.RegisterLoader("db", dbLoader)
cache.RegisterLoader("api", apiLoader)
cache.RegisterLoader("cache_fallback", fallbackLoader)

// 运行时切换数据源
user, _ := cache.Get(ctx, "key", lazycache.WithLoader("db"))
user, _ = cache.Get(ctx, "key", lazycache.WithLoader("api"))
```

#### 自定义大小估算

```go
cache := lazycache.New[*User](
    lazycache.WithSizeEstimator(func(u *User) int64 {
        return int64(len(u.Name) + len(u.Email) + 100)
    }),
)
```

#### 刷新失败处理

后台刷新失败时：
- 保留缓存中的旧值
- 将过期时间延长 50% 的 TTL
- `RefreshFail` 计数器递增

```go
stats := cache.Stats()
if stats.RefreshFail > 0 {
    log.Printf("后台刷新失败次数: %d", stats.RefreshFail)
}
```

#### 热更新配置

```go
// 初始使用较小的缓存
cache := lazycache.New[string](
    lazycache.WithMaxItems[string](1000),
    lazycache.WithTTL[string](1*time.Minute),
)

// 之后动态扩容
cache.UpdateConfig(
    lazycache.WithMaxItems[string](10000),
    lazycache.WithTTL[string](10*time.Minute),
)
```

## 性能测试

在 Apple M3 Max 上的基准测试结果：

```
BenchmarkCacheGet-16          19423137    61.14 ns/op    32 B/op    1 allocs/op
BenchmarkCacheSet-16          17817249    68.44 ns/op    16 B/op    1 allocs/op
BenchmarkCacheConcurrent-16    5973404   180.0  ns/op    32 B/op    1 allocs/op
```

**吞吐量：**
- 单线程 Get：**1600 万 ops/sec**
- 单线程 Set：**1400 万 ops/sec**
- 并发 Get：**550 万 ops/sec**

## 设计决策

### 为什么只支持字符串 Key？

虽然 Go 泛型支持 `comparable` 类型，我们将 Key 限制为 `string` 是因为：

1. **简洁性** — 大多数缓存场景使用字符串 Key
2. **性能** — Go 对字符串比较有高度优化
3. **序列化** — 便于与外部缓存集成（Redis、Memcached）
4. **前瞻性** — 更易于后续添加分布式缓存支持

### 为什么默认异步模式？

异步（懒加载）作为默认模式，原因如下：
- **更好的用户体验** — 缓存过期时不阻塞请求
- **优雅降级** — 慢速后端不影响响应延迟
- **生产级特性** — 符合高性能系统的常见行为

以下情况建议切换到同步模式：
- 必须保证数据最新
- 加载速度很快（< 10ms）
- 业务不接受旧数据

### 刷新失败策略

后台刷新失败时，选择**延长过期时间 50%** 而非直接删除，原因：
- 旧数据优于无数据（可用性 > 一致性）
- 避免故障级联到后端
- 给后端留有恢复时间

## 测试

```bash
# 运行测试
go test -v

# 开启竞态检测
go test -race -v

# 运行基准测试
go test -bench=. -benchmem

# 查看覆盖率
go test -cover
```

**测试覆盖率：** 88.1%

## 已知限制

- Key 必须是字符串类型（不支持任意可比较类型）
- 不支持分布式缓存（仅单节点）
- 无持久化（纯内存）
- 大小估算为近似值
- TTL 精度取决于实现

## 未来规划

潜在的改进方向（尚未实现）：

- 分布式缓存支持（Redis/Memcached 后端）
- 监控指标导出（Prometheus、StatsD）
- 缓存预热 / 预加载
- 压缩支持
- 多级缓存（L1/L2）
- 事件钩子（onEvict、onLoad 等）

## 贡献

目前为个人项目，欢迎提 Issue 和建议！

## 许可证

MIT License — 详见 LICENSE 文件

## 相关项目

- [groupcache](https://github.com/golang/groupcache) — Google 的分布式缓存
- [ristretto](https://github.com/dgraph-io/ristretto) — Dgraph 出品的高性能缓存
- [bigcache](https://github.com/allegro/bigcache) — 快速并发缓存

## 为什么选择 LazyCache？

与其他缓存不同，LazyCache 优先保障**用户侧延迟**，而非数据新鲜度：

| 特性 | LazyCache | 传统缓存 |
|------|-----------|---------|
| 缓存过期读取 | 立即返回旧值 | 阻塞等待重新加载 |
| 缓存击穿 | 单 Key 级别加载去重 | N 个请求同时穿透 |
| 缺失 Key 攻击 | 缓存空值 | 每次请求都打到后端 |
| 配置更新 | 热更新 | 需要重启 |
| 并发性 | 读操作无锁* | 全局锁或分片锁 |

*缓存命中时无锁；未命中时使用细粒度锁。

**适用场景：**
- 有 SLA 要求的 Web API
- 高 QPS 服务
- 后端响应较慢的系统
- 依赖外部服务的微服务

**不适用场景：**
- 强一致性要求
- 金融/交易系统
- 数据量极小、可全量缓存的场景

---

用 ❤️ 构建，零外部依赖。
