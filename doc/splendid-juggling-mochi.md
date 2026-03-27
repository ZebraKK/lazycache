# LazyCache 需求文档完善建议

## 当前文档分析

已有内容覆盖：
- ✅ 基本目标和定位
- ✅ 核心特性（LRU、懒加载、多源更新）
- ✅ 技术栈约束（Go + 官方包）

## 建议补充的内容

### 1. 接口设计细节

**需要明确：**
- `Get()` 接口的完整签名和返回值
- `Set()` / `Update()` 接口的参数设计
- `Invalidate()` 接口的语义（立即删除 vs 标记过期）
- 更新源（Loader）的接口定义
- 配置更新接口（热配置如何实现）

### 2. 并发安全策略

**需要说明：**
- 多 goroutine 并发读写的安全性保证
- 同一 key 同时过期时的更新去重机制（防止缓存击穿）
- 更新过程中的锁粒度设计（全局锁 vs 分片锁 vs 单 key 锁）

### 3. 过期和刷新策略

**需要明确：**
- TTL 的设置方式（全局默认 + item 级别覆盖？）
- 懒加载的触发时机和边界条件
- 后台刷新失败时的降级策略
- 是否支持主动预热（提前加载热点数据）

### 4. 监控和可观测性

**建议增加：**
- 缓存命中率统计
- 内存使用监控
- 更新延迟和失败率
- 慢查询告警
- Debug 模式和日志级别

### 5. 错误处理

**需要定义：**
- 更新源返回错误时的行为（继续返回旧值？返回错误？）
- 缓存满时的写入失败处理
- 并发更新冲突的解决方案

### 6. 性能指标

**建议设定：**
- 单机 QPS 目标（如 100k+ ops/sec）
- 内存占用预期
- Get 操作延迟要求（P99 < 1ms）
- 后台刷新的并发度控制

### 7. 使用示例

**补充代码示例：**
```go
// 基本使用
cache := lazycache.New(WithSize(1000), WithTTL(time.Minute))

// 注册更新源
cache.RegisterLoader("db", func(ctx context.Context, key string) (interface{}, error) {
    return db.Query(key)
})

// 获取数据（懒加载）
value, err := cache.Get(ctx, "user:123", WithLoader("db"))

// 同步更新
value, err := cache.Get(ctx, "user:123", WithLoader("db"), WithSync())

// 热更新配置
cache.UpdateConfig(WithSize(2000))
```

### 8. 测试策略

**建议包含：**
- 单元测试覆盖率目标（>80%）
- 并发竞态测试（race detector）
- 压力测试场景
- 内存泄漏检测

### 9. 边界场景处理

**需要考虑：**
- key 不存在时的处理
- 空值缓存（避免缓存穿透）
- 大对象的序列化策略
- 缓存雪崩的预防

### 10. 项目结构建议

```
lazycache/
├── cache.go          # 主缓存逻辑
├── item.go           # Item 接口定义
├── loader.go         # 更新源接口
├── config.go         # 配置管理
├── lru.go            # LRU 淘汰策略
├── metrics.go        # 监控指标
├── options.go        # 可选参数
└── cache_test.go     # 测试用例
```

## 已确定的设计决策

1. **Item 通用格式**：✅ 已确定
   - **V1 版本**：使用泛型（Go 1.18+），类型安全优先
   - **V2 版本**：提供混合方案（泛型包装层 + interface{} 底层），兼顾两者优势

2. **配置热更新**：✅ 已确定
   - TTL 变化后，已有 item 保持在内存中
   - 访问时使用新的 TTL 进行过期判断
   - 老数据不主动清理，依靠 LRU 淘汰

3. **多更新源策略**：✅ 已确定
   - 不支持 fallback 降级机制
   - 仅支持 switch 切换（手动指定使用哪个源）

4. **内存限制**：✅ 已确定
   - 主要限制：按 item 数量（如 10000 个）
   - 托底限制：按字节大小（如最大 1GB）
   - 超过任一限制都触发 LRU 淘汰

5. **空值缓存**：✅ 已确定
   - 需要支持空值缓存（防止缓存穿透）
   - 查询不存在的 key 时，缓存 nil 或特殊标记值

## V1 核心接口设计（基于泛型）

### 1. 主要类型定义

```go
// Cache 主缓存结构（泛型）
type Cache[K comparable, V any] struct {
    mu          sync.RWMutex
    items       map[K]*item[V]
    maxItems    int           // 主限制：最大 item 数量
    maxBytes    int64         // 托底限制：最大字节数
    currentSize int64         // 当前占用字节数
    ttl         time.Duration // 默认 TTL
    lru         *lruList[K]   // LRU 淘汰链表
    loaders     map[string]Loader[K, V] // 多个更新源
}

// item 缓存项
type item[V any] struct {
    value      V
    expireAt   time.Time
    size       int64         // 估算的字节大小
    loading    bool          // 是否正在后台更新
    loadChan   chan struct{} // 同步等待更新完成的通道
    isNull     bool          // 标记空值（用于缓存穿透）
}

// Loader 更新源接口
type Loader[K comparable, V any] interface {
    Load(ctx context.Context, key K) (V, error)
}

// LoaderFunc 函数式 Loader
type LoaderFunc[K comparable, V any] func(ctx context.Context, key K) (V, error)
```

### 2. 核心 API

```go
// New 创建缓存实例
func New[K comparable, V any](opts ...Option[K, V]) *Cache[K, V]

// Get 获取缓存项（懒加载模式）
func (c *Cache[K, V]) Get(ctx context.Context, key K, opts ...GetOption) (V, error)

// Set 设置缓存项
func (c *Cache[K, V]) Set(key K, value V, opts ...SetOption)

// Invalidate 使缓存项失效
func (c *Cache[K, V]) Invalidate(key K)

// RegisterLoader 注册更新源
func (c *Cache[K, V]) RegisterLoader(name string, loader Loader[K, V])

// UpdateConfig 热更新配置
func (c *Cache[K, V]) UpdateConfig(opts ...Option[K, V])

// Stats 获取统计信息
func (c *Cache[K, V]) Stats() Statistics
```

### 3. 可选参数设计

```go
// Option 缓存配置选项
type Option[K comparable, V any] func(*Cache[K, V])

func WithMaxItems[K comparable, V any](n int) Option[K, V]
func WithMaxBytes[K comparable, V any](bytes int64) Option[K, V]
func WithTTL[K comparable, V any](d time.Duration) Option[K, V]
func WithSizeEstimator[K comparable, V any](fn SizeEstimator[V]) Option[K, V]

// GetOption 获取时的选项
type GetOption func(*getOptions)

func WithLoader(name string) GetOption        // 指定使用哪个更新源
func WithSync() GetOption                     // 同步模式（阻塞等待更新）
func WithAsync() GetOption                    // 异步模式（返回旧值，后台刷新）
func WithTTLOverride(d time.Duration) GetOption // 覆盖默认 TTL
```

### 4. 懒加载核心逻辑

```go
func (c *Cache[K, V]) Get(ctx context.Context, key K, opts ...GetOption) (V, error) {
    options := parseGetOptions(opts)

    c.mu.RLock()
    it, exists := c.items[key]
    c.mu.RUnlock()

    // 情况1: 缓存命中且未过期
    if exists && !it.isExpired() {
        c.lru.Touch(key) // 更新 LRU
        if it.isNull {
            return zero[V](), ErrNotFound // 返回空值缓存
        }
        return it.value, nil
    }

    // 情况2: 缓存未命中或已过期
    loader := c.getLoader(options.loaderName)
    if loader == nil {
        return zero[V](), ErrNoLoader
    }

    // 情况2a: 有旧值且为异步模式（懒加载核心）
    if exists && options.mode == AsyncMode {
        c.asyncRefresh(ctx, key, loader) // 后台刷新
        if it.isNull {
            return zero[V](), ErrNotFound
        }
        return it.value, nil // 立即返回旧值
    }

    // 情况2b: 同步模式或无旧值
    return c.syncLoad(ctx, key, loader) // 阻塞加载
}
```

### 5. 并发安全设计

**核心策略：单 key 级别的加载去重**

```go
// 同一个 key 同时只允许一个 goroutine 执行加载
func (c *Cache[K, V]) syncLoad(ctx context.Context, key K, loader Loader[K, V]) (V, error) {
    c.mu.Lock()

    it, exists := c.items[key]

    // 检查是否已有其他 goroutine 在加载
    if exists && it.loading {
        ch := it.loadChan
        c.mu.Unlock()

        // 等待加载完成
        select {
        case <-ch:
            c.mu.RLock()
            defer c.mu.RUnlock()
            return c.items[key].value, nil
        case <-ctx.Done():
            return zero[V](), ctx.Err()
        }
    }

    // 当前 goroutine 负责加载
    if !exists {
        it = &item[V]{loadChan: make(chan struct{})}
        c.items[key] = it
    }
    it.loading = true
    c.mu.Unlock()

    // 释放锁后执行耗时的加载操作
    value, err := loader.Load(ctx, key)

    c.mu.Lock()
    defer c.mu.Unlock()

    if err != nil {
        // 加载失败：标记为空值缓存（防穿透）
        it.isNull = true
        it.expireAt = time.Now().Add(c.ttl)
    } else {
        // 加载成功
        it.value = value
        it.isNull = false
        it.expireAt = time.Now().Add(c.ttl)
        it.size = c.estimateSize(value)
        c.currentSize += it.size
    }

    it.loading = false
    close(it.loadChan) // 通知等待的 goroutines

    c.evictIfNeeded() // 检查是否需要淘汰

    if it.isNull {
        return zero[V](), err
    }
    return it.value, nil
}
```

### 6. LRU 淘汰策略

```go
func (c *Cache[K, V]) evictIfNeeded() {
    // 双重限制检查
    for len(c.items) > c.maxItems || c.currentSize > c.maxBytes {
        // 从 LRU 链表尾部获取最少使用的 key
        victim := c.lru.RemoveLast()
        if victim == nil {
            break
        }

        it := c.items[victim]
        c.currentSize -= it.size
        delete(c.items, victim)
    }
}
```

### 7. 大小估算策略

```go
type SizeEstimator[V any] func(V) int64

// 默认估算器（使用反射，性能较慢）
func defaultSizeEstimator[V any](v V) int64 {
    return int64(unsafe.Sizeof(v)) // 简化版
}

// 用户可提供自定义估算器
cache := New[string, *User](
    WithSizeEstimator(func(u *User) int64 {
        return int64(len(u.Name) + len(u.Email) + 100)
    }),
)
```

### 8. 使用示例

```go
// 创建缓存
cache := lazycache.New[string, *User](
    lazycache.WithMaxItems[string, *User](10000),
    lazycache.WithMaxBytes[string, *User](1<<30), // 1GB
    lazycache.WithTTL[string, *User](5*time.Minute),
)

// 注册数据源
cache.RegisterLoader("db", lazycache.LoaderFunc[string, *User](
    func(ctx context.Context, key string) (*User, error) {
        return db.QueryUser(key)
    },
))

cache.RegisterLoader("api", lazycache.LoaderFunc[string, *User](
    func(ctx context.Context, key string) (*User, error) {
        return api.FetchUser(key)
    },
))

// 异步获取（懒加载，默认模式）
user, err := cache.Get(ctx, "user:123", lazycache.WithLoader("db"))
// 即使过期也立即返回旧值，后台异步刷新

// 同步获取（阻塞等待）
user, err := cache.Get(ctx, "user:456",
    lazycache.WithLoader("db"),
    lazycache.WithSync(),
)

// 手动设置
cache.Set("user:789", &User{ID: "789", Name: "Alice"})

// 切换数据源
user, err := cache.Get(ctx, "user:123", lazycache.WithLoader("api"))

// 使缓存失效
cache.Invalidate("user:123")

// 热更新配置
cache.UpdateConfig(
    lazycache.WithMaxItems[string, *User](20000),
    lazycache.WithTTL[string, *User](10*time.Minute),
)
```

## 项目结构设计

```
lazycache/
├── go.mod
├── go.sum
├── README.md
├── cache.go          # 主缓存逻辑和公共 API
├── cache_test.go     # 单元测试
├── item.go           # item 结构体和方法
├── lru.go            # LRU 淘汰链表实现
├── lru_test.go
├── loader.go         # Loader 接口和实现
├── options.go        # Option 模式相关
├── stats.go          # 统计信息
├── size.go           # 大小估算相关
├── examples/         # 使用示例
│   ├── basic/
│   ├── multi_loader/
│   └── benchmark/
└── docs/
    └── design.md     # 详细设计文档
```

## 待确认的细节问题

在开始实现前，还有几个细节需要用户确认：

### 问题1：空值缓存的 TTL 策略
查询不存在的 key 时，这个"空结果"应该缓存多久？

- **选项A（推荐）**：与正常数据相同的 TTL
  - 使用统一的过期时间，简单一致
  - 适合数据变化不频繁的场景

- **选项B**：单独配置较短的 TTL（如 1 分钟）
  - 空值更快过期，避免长时间缓存错误状态
  - 适合数据可能随时新增的场景

- **选项C**：不缓存空值，每次都查询
  - 不缓存 NotFound 结果
  - 但这样可能导致缓存穿透问题

### 问题2：后台异步刷新失败时的处理策略
当懒加载触发后台更新，但更新源返回错误时应该？

- **选项A（推荐）**：保留旧值，延长过期时间
  - 继续使用旧数据，避免雪崩
  - 等下次访问再重试，适合高可用场景

- **选项B**：删除缓存，标记为空值
  - 严格遵循数据一致性
  - 下次访问会重新加载，可能导致频繁失败

- **选项C**：保留旧值，不改变过期时间
  - 维持旧值但仍然过期
  - 下次访问继续尝试刷新

### 问题3：默认的异步/同步模式
当用户调用 Get() 但不指定 WithSync/WithAsync 时，应该？

- **选项A（推荐）**：默认异步（懒加载）
  - 强调低延迟，优先返回旧值
  - 符合 lazycache 的核心理念

- **选项B**：默认同步
  - 强调数据新鲜度，阻塞等待
  - 更保守，用户需要显式选择异步

- **选项C**：智能选择
  - 缓存未命中时同步，过期时异步
  - 兼顾两者，但逻辑更复杂

### 问题4：Key 类型约束
虽然泛型支持 comparable，但实际是否需要限制 Key 必须是 string？

- **选项A（推荐）**：限制为 string
  - 简化实现，符合大多数场景（如 Redis key）
  - V1 版本足够，可读性好

- **选项B**：支持所有 comparable 类型
  - 充分发挥泛型优势，支持 int、struct 等做 key
  - 但增加复杂度

---

**请用户选择偏好，然后进入最终计划编写阶段**
