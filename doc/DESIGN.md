# LazyCache - 懒加载缓存中间件实现计划

## Context（背景与目标）

### 为什么要做这个项目？

在高并发系统中，缓存是提升性能的关键手段。传统缓存方案存在以下痛点：

1. **缓存过期时的查询阻塞**：当缓存过期时，查询会阻塞等待数据重新加载，导致响应延迟激增
2. **缓存击穿风险**：热点数据过期时，大量并发请求同时回源，可能压垮后端服务
3. **缓存穿透问题**：恶意查询不存在的 key，导致每次都穿透到后端
4. **配置僵化**：运行时无法动态调整缓存容量和过期策略

### LazyCache 的解决方案

本项目实现一个**懒加载更新机制的缓存中间件**，核心特性：

- **零阻塞查询**：缓存过期时立即返回旧值，后台异步刷新，用户无感知
- **防击穿**：单 key 级别的加载去重，并发请求只触发一次后端查询
- **防穿透**：空值缓存机制，避免重复查询不存在的数据
- **灵活配置**：支持运行时热更新缓存大小、TTL 等参数
- **智能淘汰**：LRU 策略 + 双重内存限制（item 数量 + 字节大小）
- **多数据源**：可注册多个 Loader，运行时切换

### 技术选型

- **语言**：Go 1.18+（使用泛型实现类型安全）
- **依赖**：仅使用 Go 官方标准库，零外部依赖
- **定位**：独立 package，方便集成到任何 Go 项目

---

## 已确定的设计决策

### 核心架构决策

1. **Item 通用格式**
   - **V1 版本**：使用泛型（Go 1.18+），类型安全优先
   - **V2 版本**：提供混合方案（泛型包装层 + interface{} 底层），兼顾两者优势

2. **配置热更新**
   - TTL 变化后，已有 item 保持在内存中
   - 访问时使用新的 TTL 进行过期判断
   - 老数据不主动清理，依靠 LRU 淘汰

3. **多更新源策略**
   - 不支持 fallback 降级机制
   - 仅支持 switch 切换（手动指定使用哪个源）

4. **内存限制**
   - 主要限制：按 item 数量（如 10000 个）
   - 托底限制：按字节大小（如最大 1GB）
   - 超过任一限制都触发 LRU 淘汰

5. **空值缓存**
   - 需要支持空值缓存（防止缓存穿透）
   - 查询不存在的 key 时，缓存 nil 或特殊标记值

### 细节设计决策

1. **空值缓存 TTL**：与正常数据相同的 TTL
   - 统一的过期时间，简化逻辑

2. **后台刷新失败策略**：保留旧值，延长过期时间
   - 异步刷新失败时，延长 expireAt（如增加原 TTL 的 50%）

3. **默认模式**：默认异步（懒加载）
   - Get() 不指定选项时，过期缓存返回旧值并后台刷新

4. **Key 类型**：限制为 string
   - `Cache[V any]` 而不是 `Cache[K comparable, V any]`

---

## V1 核心接口设计（基于泛型）

### 1. 主要类型定义

```go
// Cache 主缓存结构（泛型）
type Cache[V any] struct {
    mu          sync.RWMutex
    items       map[string]*item[V]
    maxItems    int           // 主限制：最大 item 数量
    maxBytes    int64         // 托底限制：最大字节数
    currentSize int64         // 当前占用字节数
    ttl         time.Duration // 默认 TTL
    lru         *lruList      // LRU 淘汰链表
    loaders     map[string]Loader[V] // 多个更新源
    sizeEstimator SizeEstimator[V]
    stats       Statistics
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
type Loader[V any] interface {
    Load(ctx context.Context, key string) (V, error)
}

// LoaderFunc 函数式 Loader
type LoaderFunc[V any] func(ctx context.Context, key string) (V, error)
```

### 2. 核心 API

```go
// New 创建缓存实例
func New[V any](opts ...Option[V]) *Cache[V]

// Get 获取缓存项（懒加载模式）
func (c *Cache[V]) Get(ctx context.Context, key string, opts ...GetOption) (V, error)

// Set 设置缓存项
func (c *Cache[V]) Set(key string, value V, opts ...SetOption)

// Invalidate 使缓存项失效
func (c *Cache[V]) Invalidate(key string)

// RegisterLoader 注册更新源
func (c *Cache[V]) RegisterLoader(name string, loader Loader[V])

// UpdateConfig 热更新配置
func (c *Cache[V]) UpdateConfig(opts ...Option[V])

// Stats 获取统计信息
func (c *Cache[V]) Stats() Statistics
```

### 3. 可选参数设计

```go
// Option 缓存配置选项
type Option[V any] func(*Cache[V])

func WithMaxItems[V any](n int) Option[V]
func WithMaxBytes[V any](bytes int64) Option[V]
func WithTTL[V any](d time.Duration) Option[V]
func WithSizeEstimator[V any](fn SizeEstimator[V]) Option[V]

// GetOption 获取时的选项
type GetOption func(*getOptions)

func WithLoader(name string) GetOption        // 指定使用哪个更新源
func WithSync() GetOption                     // 同步模式（阻塞等待更新）
func WithAsync() GetOption                    // 异步模式（返回旧值，后台刷新）
```

### 4. 使用示例

```go
// 创建缓存
cache := lazycache.New[*User](
    lazycache.WithMaxItems[*User](10000),
    lazycache.WithMaxBytes[*User](1<<30), // 1GB
    lazycache.WithTTL[*User](5*time.Minute),
)

// 注册数据源
cache.RegisterLoader("db", lazycache.LoaderFunc[*User](
    func(ctx context.Context, key string) (*User, error) {
        return db.QueryUser(key)
    },
))

// 异步获取（懒加载，默认模式）
user, err := cache.Get(ctx, "user:123", lazycache.WithLoader("db"))

// 同步获取（阻塞等待）
user, err := cache.Get(ctx, "user:456",
    lazycache.WithLoader("db"),
    lazycache.WithSync(),
)

// 手动设置
cache.Set("user:789", &User{ID: "789", Name: "Alice"})

// 热更新配置
cache.UpdateConfig(
    lazycache.WithMaxItems[*User](20000),
    lazycache.WithTTL[*User](10*time.Minute),
)
```

---

## 项目结构设计

```
lazycache/
├── go.mod              # Go module 定义，require Go 1.18+
├── README.md           # 项目说明和使用示例
├── DESIGN.md           # 本设计文档
├── DEVLOG.md           # 开发日志
├── cache.go            # 核心 Cache 结构和主要 API
├── item.go             # item 结构体和过期判断
├── lru.go              # LRU 双向链表实现
├── loader.go           # Loader 接口定义
├── options.go          # Option 函数模式
├── stats.go            # 统计信息结构
├── errors.go           # 错误定义
├── cache_test.go       # 单元测试
└── examples/           # 使用示例
    ├── basic/
    ├── multi_loader/
    └── benchmark/
```

---

## 实施计划

### Phase 1: 初始化项目结构

- 创建 `go.mod`（Go 1.18+）
- 初始化基本文件结构
- 编写 README.md 和文档

### Phase 2: 核心实现

#### 2.1 类型定义
- `cache.go`: Cache 结构体
- `item.go`: item 结构体和过期判断
- `errors.go`: 错误定义

#### 2.2 LRU 实现
- `lru.go`: 双向链表实现

#### 2.3 核心逻辑
- `cache.go`: Get/Set/Invalidate 实现
- 懒加载逻辑
- 同步加载与去重（防击穿）
- 异步刷新
- LRU 淘汰

### Phase 3: 辅助功能

- `loader.go`: Loader 接口
- `options.go`: Option 模式
- `stats.go`: 统计功能

### Phase 4: 测试

- 基本功能测试
- LRU 淘汰测试
- 懒加载异步刷新测试
- 并发加载去重测试
- 空值缓存测试
- 双重内存限制测试
- 配置热更新测试
- 并发竞态测试 (`go test -race`)

### Phase 5: 示例和文档

- 创建示例代码
- 完善 README
- 性能基准测试

---

## 验证测试方案

### 1. 功能测试
```bash
go test -v -cover ./...
```

### 2. 并发竞态测试
```bash
go test -race -v ./...
```

### 3. 基准测试
```bash
go test -bench=. -benchmem
```

### 4. 手动验证场景
- 懒加载：设置短 TTL，观察异步刷新行为
- LRU：设置小容量，验证淘汰最少使用项
- 击穿防护：并发请求同一个未缓存 key，验证只加载一次
- 空值缓存：查询不存在的 key，验证防穿透效果
- 热配置：运行时修改 maxItems，观察生效

---

## 关键文件路径

实现时需要创建的文件：
- `/Users/xiaowyu/xwill/lazycache/go.mod` - Go module 初始化
- `/Users/xiaowyu/xwill/lazycache/cache.go` - 核心缓存逻辑（约 300 行）
- `/Users/xiaowyu/xwill/lazycache/item.go` - Item 定义（约 30 行）
- `/Users/xiaowyu/xwill/lazycache/lru.go` - LRU 实现（约 100 行）
- `/Users/xiaowyu/xwill/lazycache/loader.go` - Loader 接口（约 20 行）
- `/Users/xiaowyu/xwill/lazycache/options.go` - Options 定义（约 80 行）
- `/Users/xiaowyu/xwill/lazycache/stats.go` - 统计功能（约 50 行）
- `/Users/xiaowyu/xwill/lazycache/errors.go` - 错误定义（约 15 行）
- `/Users/xiaowyu/xwill/lazycache/cache_test.go` - 单元测试（约 400 行）

预计总代码量：约 1000 行（不含示例和文档）
