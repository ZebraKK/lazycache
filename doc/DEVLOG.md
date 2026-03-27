# LazyCache 开发日志

> 记录项目开发过程中的进度、问题和决策

---

## 2026-03-20 项目规划阶段

### ✅ 完成事项

1. **需求分析与设计决策**
   - 确定使用泛型方案（Go 1.18+）作为 V1 版本
   - 确定 Key 类型限制为 string
   - 确定默认异步（懒加载）模式
   - 确定空值缓存 TTL 策略：与正常数据相同
   - 确定后台刷新失败策略：保留旧值并延长过期时间

2. **核心架构设计**
   - 定义主要类型：Cache[V any], item[V any]
   - 设计 Loader 接口
   - 设计 Option 模式的 API
   - 设计懒加载核心逻辑流程
   - 设计并发安全策略（单 key 级别加载去重）

3. **文档编写**
   - 创建 DESIGN.md：详细设计文档
   - 创建 DEVLOG.md：开发日志（本文件）
   - 更新 README.md：项目介绍

### 📋 设计亮点

1. **零阻塞查询**
   - 缓存过期时立即返回旧值
   - 后台 goroutine 异步刷新
   - 用户无感知的更新机制

2. **防缓存击穿**
   - 使用 loadChan 实现单 key 级别的加载去重
   - 多个并发请求同一个未缓存 key 时，只有一个 goroutine 加载
   - 其他 goroutine 等待加载完成

3. **双重内存限制**
   - 主限制：按 item 数量
   - 托底限制：按字节大小
   - LRU 淘汰策略

4. **类型安全**
   - 使用 Go 1.18+ 泛型
   - 编译时类型检查
   - 无需类型断言

### 🎯 计划执行情况

- [x] Phase 1: 初始化项目结构 ✅
  - [x] 创建 go.mod
  - [x] 创建基本文件骨架

- [x] Phase 2: 核心实现 ✅
  - [x] 实现 LRU 链表
  - [x] 实现 Cache 核心逻辑
  - [x] 实现懒加载机制
  - [x] 实现并发安全

- [x] Phase 3: 辅助功能 ✅
  - [x] 实现 Options 模式
  - [x] 实现统计功能
  - [x] 实现错误定义

- [x] Phase 4: 测试 ✅
  - [x] 编写单元测试
  - [x] 并发竞态测试
  - [x] 性能基准测试

- [x] Phase 5: 示例和文档 ✅
  - [x] 创建示例代码
  - [x] 完善文档

---

## 开发指南

### 编码规范

1. **命名规范**
   - 公共 API 使用大写开头（如 `New`, `Get`, `Set`）
   - 内部函数使用小写开头（如 `syncLoad`, `asyncRefresh`）
   - 接口名使用 `-er` 后缀（如 `Loader`）

2. **注释规范**
   - 所有公共 API 必须有完整的 godoc 注释
   - 复杂逻辑添加行内注释说明

3. **错误处理**
   - 定义明确的错误类型（如 `ErrNotFound`, `ErrNoLoader`）
   - 错误信息应清晰描述问题

4. **并发安全**
   - 所有共享状态访问必须加锁
   - 避免持锁执行耗时操作
   - 注意死锁风险

### 测试策略

1. **单元测试**
   - 覆盖率目标：>80%
   - 测试所有公共 API
   - 测试边界条件

2. **并发测试**
   - 使用 `go test -race` 检测竞态
   - 测试高并发场景
   - 验证加载去重机制

3. **性能测试**
   - 使用 benchmark 测试关键路径
   - 关注 Get 操作延迟
   - 监控内存分配

### 性能目标

- **QPS**: 100k+ ops/sec（单机）
- **延迟**: P99 < 1ms（Get 操作）
- **内存**: 可配置上限，精确控制

---

## 2026-03-20 实现阶段

### ✅ 完成事项

#### Phase 1: 项目结构初始化

1. **创建 go.mod**
   - 定义 module 路径：`github.com/ZebraKK/lazycache`
   - 要求 Go 1.18+（泛型支持）

2. **创建核心文件**
   - `errors.go` - 错误定义（15 行）
   - `item.go` - 缓存项结构（18 行）
   - `loader.go` - Loader 接口（16 行）
   - `lru.go` - LRU 链表实现（105 行）
   - `stats.go` - 统计功能（100 行）
   - `options.go` - Options 模式（115 行）
   - `cache.go` - 核心缓存逻辑（320 行）

#### Phase 2: 核心实现

1. **LRU 双向链表**
   - 实现了 `Touch()` 方法：将 key 移到最前端
   - 实现了 `RemoveLast()` 方法：移除最少使用的项
   - 线程安全：内部使用 mutex 保护

2. **懒加载核心逻辑**
   - `syncLoad()`: 同步加载，带防击穿保护
   - `asyncRefresh()`: 异步刷新，立即返回旧值
   - 默认异步模式，可通过 `WithSync()` 切换

3. **并发安全设计**
   - 使用 `sync.RWMutex` 保护共享状态
   - 单 key 级别的加载去重（使用 `loadChan`）
   - 避免持锁执行耗时操作

4. **双重内存限制**
   - `maxItems`: 最大项数
   - `maxBytes`: 最大字节数
   - 超过任一限制触发 LRU 淘汰

#### Phase 3: 辅助功能

1. **Options 模式**
   - `WithMaxItems`, `WithMaxBytes`, `WithTTL`
   - `WithLoader`, `WithSync`, `WithAsync`
   - `WithTTLOverride`, `WithSetTTL`

2. **统计功能**
   - 命中/未命中计数
   - 淘汰计数
   - 刷新成功/失败计数
   - 命中率计算

3. **错误定义**
   - `ErrNotFound`: key 不存在
   - `ErrNoLoader`: 未指定 loader
   - `ErrLoaderNotFound`: loader 不存在

#### Phase 4: 测试

1. **单元测试（14 个测试用例）**
   - ✅ TestBasicGetSet - 基本操作
   - ✅ TestCacheMissWithLoader - 缓存未命中
   - ✅ TestLazyLoading - 懒加载机制
   - ✅ TestAntiStampede - 防击穿
   - ✅ TestNullValueCaching - 空值缓存
   - ✅ TestLRUEvictionByCount - 按数量淘汰
   - ✅ TestLRUEvictionBySize - 按大小淘汰
   - ✅ TestInvalidate - 失效操作
   - ✅ TestConfigUpdate - 配置热更新
   - ✅ TestTTLOverride - TTL 覆盖
   - ✅ TestRefreshFailure - 刷新失败处理
   - ✅ TestMultipleLoaders - 多数据源
   - ✅ TestContextCancellation - Context 取消
   - ✅ TestConcurrentAccess - 并发访问

2. **测试结果**
   - ✅ 所有测试通过
   - ✅ 代码覆盖率：**88.1%**
   - ✅ Race detector 通过（无竞态条件）

3. **性能基准测试**
   ```
   BenchmarkCacheGet-16          19423137    61.14 ns/op    32 B/op    1 allocs/op
   BenchmarkCacheSet-16          17817249    68.44 ns/op    16 B/op    1 allocs/op
   BenchmarkCacheConcurrent-16    5973404   180.0  ns/op    32 B/op    1 allocs/op
   ```
   - Get 操作：**16M ops/sec**
   - Set 操作：**14M ops/sec**
   - 并发 Get：**5.5M ops/sec**

#### Phase 5: 文档和示例

1. **README.md**
   - 完整的功能介绍
   - API 文档
   - 使用示例
   - 性能数据
   - 设计决策说明

2. **示例代码**
   - `examples/basic/main.go` - 基础用法演示
   - 演示懒加载、防击穿、统计等功能

---

## 问题与解决方案

### [2026-03-20] 问题 1：并发测试时 channel 重复关闭

**问题描述**：
运行 `TestConcurrentAccess` 时出现 `panic: close of closed channel` 错误。

**原因分析**：
在 `syncLoad()` 方法中，当 item 已存在但未在加载状态时，直接设置 `loading = true`，但没有重新创建 `loadChan`。如果该 channel 在之前的加载中已经关闭，再次关闭会导致 panic。

**解决方案**：
在标记 `loading = true` 时，始终创建新的 `loadChan`：

```go
// 修改前
if !exists {
    it = &item[V]{loadChan: make(chan struct{})}
    c.items[key] = it
}
it.loading = true

// 修改后
if !exists {
    it = &item[V]{}
    c.items[key] = it
}
it.loading = true
it.loadChan = make(chan struct{})  // 总是创建新的 channel
```

**相关代码**：
- 文件：cache.go
- 行号：203-209

---

### [2026-03-20] 问题 2：Race detector 检测到数据竞争

**问题描述**：
运行 `go test -race` 时，在 `TestLazyLoading` 中检测到数据竞争：
```
WARNING: DATA RACE
Write at 0x00c0000bc280 by goroutine 10:
  asyncRefresh() /cache.go:279
Previous read at 0x00c0000bc280 by goroutine 8:
  Get() /cache.go:88
```

**原因分析**：
在 `Get()` 方法中，启动 `asyncRefresh` goroutine 后，主 goroutine 立即读取 `it.isNull` 和 `it.value`，而 `asyncRefresh` 可能同时在写这些字段，导致数据竞争。

**解决方案**：
在启动异步刷新之前，先捕获当前状态的快照：

```go
// 修改前
if exists && options.mode == AsyncMode {
    go c.asyncRefresh(context.Background(), key, loader, options.ttlOverride)
    if it.isNull {
        return zero[V](), ErrNotFound
    }
    return it.value, nil
}

// 修改后
if exists && options.mode == AsyncMode {
    // 捕获当前状态快照，避免数据竞争
    isNull := it.isNull
    value := it.value
    go c.asyncRefresh(context.Background(), key, loader, options.ttlOverride)
    if isNull {
        return zero[V](), ErrNotFound
    }
    return value, nil
}
```

**相关代码**：
- 文件：cache.go
- 行号：82-90

---

### [2026-03-20] 问题 3：类型推断冗余警告

**问题描述**：
编译器提示多处 `unnecessary type arguments`，例如：
```go
lazycache.WithMaxItems[string](1000)
```

**原因分析**：
Go 1.18+ 的类型推断可以从函数参数自动推导泛型参数，无需显式指定。

**解决方案**：
这只是一个警告，不影响功能。可以简化为：
```go
// 显式指定（当前做法，更清晰）
lazycache.WithMaxItems[string](1000)

// 类型推断（更简洁）
lazycache.WithMaxItems(1000)
```

选择保留显式指定，因为：
1. 更清晰地表达意图
2. 避免类型推断错误
3. 提高代码可读性

**相关代码**：
- 文件：cache_test.go, examples/basic/main.go
- 多处 Options 调用

---

## 版本规划

### V1.0.0（当前版本）

**目标**：基本功能完整，性能稳定

**核心特性**：
- ✅ 泛型 API（Go 1.18+）
- ✅ 懒加载机制
- ✅ LRU 淘汰
- ✅ 空值缓存
- ✅ 多数据源
- ✅ 并发安全
- ✅ 统计功能

**不包含**：
- interface{} 兼容层（V2）
- 分布式支持（V3）
- 持久化（V3）

### V2.0.0（未来版本）

**新增特性**：
- 混合方案：泛型包装层 + interface{} 底层
- 支持 Go 1.16+ 版本
- 更灵活的类型支持

### V3.0.0（远期规划）

**新增特性**：
- 分布式缓存支持
- 持久化存储
- 更多淘汰策略（LFU, ARC）

---

## 参考资料

- [Go 泛型教程](https://go.dev/doc/tutorial/generics)
- [Effective Go](https://go.dev/doc/effective_go)
- [Go 并发模式](https://go.dev/blog/pipelines)
- [缓存设计最佳实践](https://redis.io/docs/manual/patterns/)

---

## 更新日志

| 日期 | 版本 | 更新内容 | 责任人 |
|------|------|----------|--------|
| 2026-03-20 | v0.1.0 | 项目规划，设计文档完成 | Claude |
| 2026-03-20 | v1.0.0 | 核心功能实现完成，所有测试通过 | Claude |

---

## 项目总结

### 📊 项目统计

**代码行数**：
- 核心代码：~700 行
- 测试代码：~450 行
- 示例代码：~130 行
- 文档：~1500 行
- **总计**：~2780 行

**文件结构**：
```
lazycache/
├── go.mod                  # Module 定义
├── README.md               # 项目文档
├── DESIGN.md              # 设计文档
├── DEVLOG.md              # 开发日志（本文件）
├── cache.go               # 核心缓存逻辑（320 行）
├── item.go                # Item 定义（18 行）
├── lru.go                 # LRU 实现（105 行）
├── loader.go              # Loader 接口（16 行）
├── options.go             # Options 模式（115 行）
├── stats.go               # 统计功能（100 行）
├── errors.go              # 错误定义（15 行）
├── cache_test.go          # 单元测试（450 行）
└── examples/
    └── basic/
        ├── go.mod
        └── main.go        # 基础示例（130 行）
```

**测试覆盖率**：88.1%

**性能指标**：
- Get 延迟：61ns
- Set 延迟：68ns
- 并发 Get 延迟：180ns
- 单线程 QPS：16M ops/sec
- 并发 QPS：5.5M ops/sec

### ✨ 核心成就

1. **零阻塞查询**
   - 缓存过期时立即返回旧值
   - 后台异步刷新
   - 用户体验优秀

2. **防击穿机制**
   - 单 key 级别加载去重
   - 使用 channel 同步等待
   - 并发测试验证有效

3. **高性能实现**
   - 16M ops/sec 吞吐量
   - 61ns 延迟
   - 无外部依赖

4. **类型安全**
   - Go 1.18+ 泛型
   - 编译时类型检查
   - 零运行时类型断言

5. **完整测试**
   - 14 个测试用例
   - Race detector 验证
   - Benchmark 性能测试

### 🎓 经验总结

#### 技术收获

1. **Go 泛型使用**
   - 泛型参数传递
   - 类型约束
   - 零值处理

2. **并发编程**
   - 细粒度锁控制
   - Channel 同步模式
   - Race condition 检测和修复

3. **性能优化**
   - 避免持锁执行耗时操作
   - 使用 RWMutex 提升读性能
   - 减少内存分配

4. **API 设计**
   - Options 模式
   - 函数式 API
   - 类型推断友好

#### 设计模式

1. **Options 模式**
   - 灵活的配置方式
   - 向后兼容性好
   - 类型安全

2. **策略模式**
   - Loader 接口抽象
   - 支持多数据源
   - 运行时切换

3. **观察者模式**
   - 统计信息收集
   - 事件监控

### 🚀 后续优化方向

#### 短期优化（V1.1）

1. **性能优化**
   - [ ] 分片锁减少竞争
   - [ ] 对象池复用
   - [ ] 更高效的 LRU 实现

2. **功能增强**
   - [ ] 指标导出（Prometheus）
   - [ ] 慢查询监控
   - [ ] 批量操作 API

3. **文档完善**
   - [ ] Godoc 注释
   - [ ] 更多使用示例
   - [ ] 性能调优指南

#### 中期规划（V2.0）

1. **兼容性**
   - [ ] Go 1.16+ 支持
   - [ ] interface{} 兼容层
   - [ ] 向后兼容保证

2. **高级特性**
   - [ ] 分层缓存（L1/L2）
   - [ ] 过期回调
   - [ ] 缓存预热

#### 长期愿景（V3.0）

1. **分布式支持**
   - [ ] Redis 后端
   - [ ] 一致性哈希
   - [ ] 跨节点同步

2. **持久化**
   - [ ] 磁盘缓存
   - [ ] 快照恢复
   - [ ] WAL 日志

### 💡 关键洞察

1. **懒加载是高性能的关键**
   - 消除用户感知的阻塞
   - 允许服务快速响应
   - 适合大多数场景

2. **并发安全需要精心设计**
   - 单 key 级别的锁粒度
   - Channel 是优秀的同步原语
   - Race detector 是必备工具

3. **测试驱动很重要**
   - 先写测试再实现
   - 持续运行 race detector
   - Benchmark 验证性能假设

4. **简单优于复杂**
   - 零外部依赖
   - 清晰的 API
   - 单一职责原则

### 🙏 致谢

感谢以下资源和项目的启发：
- Go 官方文档和示例
- groupcache (Google)
- ristretto (Dgraph)
- bigcache (Allegro)

---

## 项目状态：✅ V1.0.0 完成

**所有计划任务已完成，项目可以投入使用！**
