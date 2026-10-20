package session

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// ManagerConfig 是内存 Session Manager 的配置。
//
// TTL > 0 启用过期，<= 0 关闭（零值即关闭）；过期锚点为创建与每次成功提交（写续期），
// 普通读取不续期。Now 用于注入可控时钟，nil 时使用 time.Now；测试不得用 sleep 制造过期证据。
type ManagerConfig struct {
	TTL time.Duration
	Now func() time.Time
}

// Manager 是单进程内存 Session Manager：进程内的会话事实来源。
//
// 并发模型：
//   - 每会话一把可被 context 取消的执行权（容量 1 的信号量），同一会话的 Turn 严格串行，
//     不同会话互不阻塞；不存在跨会话全局锁；
//   - entries 互斥锁只保护 map 的查找 / 插入 / 删除，绝不跨越调用方回调持有；
//   - 会话数据以不可变提交快照发布（atomic.Pointer），读取不加锁、不返回未提交状态。
//
// 零值不可用，必须经 NewManager 构造。无后台清理 goroutine、无 Close：
// 过期由惰性访问与显式 PurgeExpired 回收（物理删除，进程退出同样丢失数据）。
type Manager struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

// entry 是一个会话在内存中的全部状态：执行权、已提交快照与过期时刻。
type entry struct {
	// authority 是容量 1 的信号量：持有即拥有本会话的执行权；等待方可被 context 取消。
	authority chan struct{}
	// snapshot 是不可变提交快照，只在持有执行权时整体替换，读取方无需加锁。
	snapshot atomic.Pointer[Session]
	// expiresAt 是 UnixNano 形式的过期时刻，0 表示不启用过期。
	expiresAt atomic.Int64
}

var _ Store = (*Manager)(nil)

// NewManager 构造内存 Session Manager。cfg 为零值时表示不启用过期、使用真实时钟。
func NewManager(cfg ManagerConfig) *Manager {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		ttl:     cfg.TTL,
		now:     now,
		entries: make(map[string]*entry),
	}
}

// Create 创建会话并刷新 TTL。同 ID 会话仍有效（含存在在途 Turn）时返回 ErrSessionExists；
// 同 ID 会话已过期且当前没有在途 Turn 时，旧会话被物理删除后重新创建。
func (m *Manager) Create(ctx context.Context, id string) (Session, error) {
	const op = "session.Manager.Create"
	if err := contextError(op, ctx); err != nil {
		return Session{}, err
	}
	if err := m.checkReady(op); err != nil {
		return Session{}, err
	}
	if id == "" {
		return Session{}, invalidArgumentError(op, "会话 ID 不能为空")
	}

	m.mu.Lock()
	if old, ok := m.entries[id]; ok && !m.reclaimable(old) {
		m.mu.Unlock()
		return Session{}, sessionExistsError(op, id)
	}
	created := Session{ID: id}
	m.entries[id] = newEntry(m, created)
	m.mu.Unlock()

	return cloneSession(created), nil
}

// Get 返回会话的深拷贝快照；调用方修改返回值不影响会话。
//
// 不存在返回 ErrSessionNotFound；已过期返回 ErrSessionExpired 并物理删除。
// 过期判定以「能取得执行权」为前提：取不到（存在在途 Turn）时按有效会话返回快照，
// 因此 Get 不会因为等待执行权而阻塞，也不会清理正在执行的会话。
func (m *Manager) Get(ctx context.Context, id string) (Session, error) {
	const op = "session.Manager.Get"
	if err := contextError(op, ctx); err != nil {
		return Session{}, err
	}
	if err := m.checkReady(op); err != nil {
		return Session{}, err
	}
	if id == "" {
		return Session{}, invalidArgumentError(op, "会话 ID 不能为空")
	}

	e, ok := m.lookup(id)
	if !ok {
		return Session{}, sessionNotFoundError(op, id)
	}

	select {
	case e.authority <- struct{}{}:
		defer func() { <-e.authority }()
		if m.expired(e) {
			m.remove(id, e)
			return Session{}, sessionExpiredError(op, id)
		}
		return cloneSession(*e.snapshot.Load()), nil
	default:
		// 有在途 Turn：视为活跃会话，不判定过期、不阻塞、不续期。
		return cloneSession(*e.snapshot.Load()), nil
	}
}

// Delete 在取得执行权后物理删除会话，与同一会话的 Turn / Save 互斥。
//
// 会话不存在（含已被清理）返回 ErrSessionNotFound；已过期返回 ErrSessionExpired
// 并完成物理删除。删除不影响其它会话的任何数据。
func (m *Manager) Delete(ctx context.Context, id string) error {
	const op = "session.Manager.Delete"
	e, err := m.acquire(ctx, op, id)
	if err != nil {
		return err
	}
	defer m.release(e)

	m.remove(id, e)
	return nil
}

// PurgeExpired 物理删除可清理的过期会话，返回删除数量。
//
// 只有能取得执行权的会话才会被判定过期，因此正在执行 Turn 的会话一律跳过、不续期；
// ctx 取消后立即停止扫描并返回已删除数量（本方法不产生错误返回）。
func (m *Manager) PurgeExpired(ctx context.Context) int {
	if ctx == nil || ctx.Err() != nil || m == nil || m.ttl <= 0 {
		return 0
	}

	m.mu.Lock()
	ids := make([]string, 0, len(m.entries))
	for id := range m.entries {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	purged := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		e, ok := m.lookup(id)
		if !ok {
			continue
		}
		select {
		case e.authority <- struct{}{}:
		default:
			// 在途 Turn：存在活动，跳过。
			continue
		}
		if m.expired(e) && m.remove(id, e) {
			purged++
		}
		<-e.authority
	}
	return purged
}

// RunTurn 在会话执行权内执行 fn，并把 Turn 的暂存写入整轮原子提交或整轮丢弃。
//
// 提交条件（全部成立）：ctx 未取消、fn 返回 nil、Turn 仍打开、会话仍是当前 entry、
// 提交前校验通过。失败、取消、超时与 panic 都不产生部分提交，也不刷新 TTL；
// 执行权在全部路径（含 panic）由 defer 释放。
//
// 排队等待执行权时 ctx 取消 / 超时返回 cancelled / deadline_exceeded，且 fn 不会被执行。
func (m *Manager) RunTurn(ctx context.Context, sessionID string, fn func(*Turn) error) error {
	const op = "session.Manager.RunTurn"
	if err := contextError(op, ctx); err != nil {
		return err
	}
	if fn == nil {
		return invalidArgumentError(op, "Turn 回调不能为空")
	}

	e, err := m.acquire(ctx, op, sessionID)
	if err != nil {
		return err
	}
	defer m.release(e)

	// 快照由执行权保护：持有执行权期间不会有其它提交，newTurn 内部再深拷贝一次。
	turn := newTurn(*e.snapshot.Load())
	var fnErr error
	func() {
		// fn 返回（含 panic）后 Turn 立刻关闭：此后的写入被拒绝，不参与提交。
		defer turn.close()
		fnErr = fn(turn)
	}()

	// P1：取消 / 超时优先于回调返回值，避免已取消的 Turn 借提交篡改会话状态。
	if err := contextError(op, ctx); err != nil {
		return err
	}
	// P2：回调失败则整轮丢弃。
	if fnErr != nil {
		return callbackError(op, fnErr)
	}
	return m.commitTurn(ctx, op, e, turn)
}

// Load 是 Store 的读取实现，等价于 Get（深拷贝快照，不参与执行权）。
func (m *Manager) Load(ctx context.Context, id string) (Session, error) {
	return m.Get(ctx, id)
}

// Save 是 Store 的写入实现：在执行权内用入参全量替换会话的 Messages / Summary / Todos
// （不合并、不隐式创建），成功后刷新 TTL。
//
// 非法消息序列返回 invalid_pairing / invalid_argument，非法 Todo 返回 invalid_argument，
// 二者都不会改变会话数据；会话不存在 / 已过期返回 ErrSessionNotFound / ErrSessionExpired。
func (m *Manager) Save(ctx context.Context, s Session) error {
	const op = "session.Manager.Save"
	if err := contextError(op, ctx); err != nil {
		return err
	}
	if err := m.checkReady(op); err != nil {
		return err
	}
	if s.ID == "" {
		return invalidArgumentError(op, "会话 ID 不能为空")
	}
	if err := core.ValidateMessages(s.Messages); err != nil {
		return err
	}
	for _, todo := range s.Todos {
		if err := todo.Validate(); err != nil {
			return err
		}
	}

	e, err := m.acquire(ctx, op, s.ID)
	if err != nil {
		return err
	}
	defer m.release(e)

	replacement := cloneSession(s)
	m.publish(s.ID, e, replacement)
	return nil
}

// newEntry 构造会话 entry，并按当前时钟设定首次过期时刻。
func newEntry(m *Manager, s Session) *entry {
	e := &entry{authority: make(chan struct{}, 1)}
	e.snapshot.Store(&s)
	e.refresh(m)
	return e
}

// refresh 按当前时钟把过期时刻推到 now+TTL；TTL 关闭时不写入。
func (e *entry) refresh(m *Manager) {
	if m.ttl > 0 {
		e.expiresAt.Store(m.clock().Add(m.ttl).UnixNano())
	}
}

// expired 判定会话是否已过 TTL；TTL 关闭或未设置过期时刻时永不过期。
func (m *Manager) expired(e *entry) bool {
	if m.ttl <= 0 {
		return false
	}
	expiresAt := e.expiresAt.Load()
	if expiresAt == 0 {
		return false
	}
	return !m.clock().Before(time.Unix(0, expiresAt))
}

// clock 返回当前时间，测试可注入可控时钟。
func (m *Manager) clock() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

// checkReady 拒绝未构造（零值）的 Manager，避免 nil map 写入 panic 被当作业务错误。
func (m *Manager) checkReady(op string) error {
	if m == nil || m.entries == nil {
		return invalidArgumentError(op, "Manager 必须经 NewManager 构造")
	}
	return nil
}

// lookup 在 map 锁内查一次 entry，锁不跨越任何调用方回调。
func (m *Manager) lookup(id string) (*entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	return e, ok
}

// remove 仅在 entry 仍是 map 中的当前 entry 时删除，返回是否真的删除。
func (m *Manager) remove(id string, e *entry) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[id] != e {
		return false
	}
	delete(m.entries, id)
	return true
}

// publish 在 entry 仍是当前 entry 时发布新快照并刷新 TTL，返回是否发布成功。
func (m *Manager) publish(id string, e *entry, s Session) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[id] != e {
		return false
	}
	e.snapshot.Store(&s)
	e.refresh(m)
	return true
}

// reclaimable 判断同 ID 的既有 entry 是否已过期且此刻没有在途 Turn，
// 只有这种会话才允许被 Create 替换。检查在当前 goroutine 内非阻塞完成。
func (m *Manager) reclaimable(e *entry) bool {
	if m.ttl <= 0 {
		return false
	}
	select {
	case e.authority <- struct{}{}:
		// 取得执行权即证明没有在途 Turn，可以安全判定过期。
		defer func() { <-e.authority }()
		return m.expired(e)
	default:
		return false
	}
}

// acquire 取得会话执行权，调用方必须在全部后续路径 release。
//
// 取得执行权后复核 entry 仍是当前 entry（等待期间可能被 Delete / PurgeExpired 移除），
// 并按契约判定过期：能取得执行权且已过 TTL 即确认为过期，物理删除后返回 ErrSessionExpired。
// 返回时该会话的已提交快照稳定不变（只有执行权持有者能替换它）。
func (m *Manager) acquire(ctx context.Context, op, id string) (*entry, error) {
	if err := contextError(op, ctx); err != nil {
		return nil, err
	}
	if err := m.checkReady(op); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, invalidArgumentError(op, "会话 ID 不能为空")
	}

	e, ok := m.lookup(id)
	if !ok {
		return nil, sessionNotFoundError(op, id)
	}

	select {
	case e.authority <- struct{}{}:
	case <-ctx.Done():
		return nil, contextError(op, ctx)
	}

	if current, still := m.lookup(id); !still || current != e {
		m.release(e)
		return nil, sessionNotFoundError(op, id)
	}
	if m.expired(e) {
		m.remove(id, e)
		m.release(e)
		return nil, sessionExpiredError(op, id)
	}

	return e, nil
}

// release 归还执行权。
func (m *Manager) release(e *entry) {
	<-e.authority
}

// commitTurn 执行提交前复核（P1 / P4 / P5）并在全部成立时整体发布。
func (m *Manager) commitTurn(ctx context.Context, op string, e *entry, t *Turn) error {
	messages, summary, todos := t.staged()

	// P5：对「已提交历史 + 本 Turn 暂存」再跑一次整段校验。
	if err := core.ValidateMessages(messages); err != nil {
		return err
	}
	for _, todo := range todos {
		if err := todo.Validate(); err != nil {
			return err
		}
	}

	// P1：发布前最后复核一次 context。
	if err := contextError(op, ctx); err != nil {
		return err
	}

	// P4：entry 已被移除时不提交，也不静默继续。
	if !m.publish(t.sessionID, e, Session{
		ID:       t.sessionID,
		Messages: messages,
		Summary:  summary,
		Todos:    todos,
	}) {
		return sessionNotFoundError(op, t.sessionID)
	}
	return nil
}
