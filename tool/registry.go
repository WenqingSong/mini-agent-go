package tool

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// ErrEmptyToolName 表示工具名称为空或全空白。
var ErrEmptyToolName = errors.New("工具名称不能为空")

// ErrDuplicateTool 表示同名工具已被注册。
var ErrDuplicateTool = errors.New("同名工具已注册")

// ErrInvalidToolSpec 表示工具 Spec 的 Parameters 不在支持的 JSON Schema 子集内。
var ErrInvalidToolSpec = errors.New("工具 Spec 非法")

// ErrRegistrySealed 表示注册表已封闭，不再接受注册。
var ErrRegistrySealed = errors.New("工具注册表已封闭")

const registerOp = "tool.MapRegistry.Register"

// MapRegistry 是 Registry 的内存实现：装配期可注册，运行期只读。
//
// 所有方法都可由多个 goroutine 并发调用；注册时保存 Spec 的深拷贝，
// 因此外部修改 Specs() 的返回值或工具自身的 Spec() 都不会破坏注册表内的定义。
type MapRegistry struct {
	mu     sync.RWMutex
	sealed bool
	tools  map[string]Tool
	specs  map[string]core.ToolSpec
}

var _ Registry = (*MapRegistry)(nil)

// NewMapRegistry 返回空的注册表。
func NewMapRegistry() *MapRegistry {
	return &MapRegistry{
		tools: make(map[string]Tool),
		specs: make(map[string]core.ToolSpec),
	}
}

// Register 注册工具，键为 t.Spec().Name（区分大小写、不 trim）。
//
// 拒绝语义统一为 *core.RunError{Kind: invalid_argument}，Cause 是对应哨兵错误，
// 调用方可用 errors.Is 区分「名称非法 / 重复注册 / Spec 非法 / 已封闭」。
func (r *MapRegistry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sealed {
		return registerRejection(ErrRegistrySealed, "注册表已封闭，不能再注册工具")
	}
	if t == nil {
		return registerRejection(ErrInvalidToolSpec, "工具不能为空")
	}

	spec := t.Spec()
	name := spec.Name
	if strings.TrimSpace(name) == "" {
		return registerRejection(ErrEmptyToolName, "工具名称不能为空或全空白")
	}
	if _, ok := r.tools[name]; ok {
		return registerRejection(ErrDuplicateTool, fmt.Sprintf("工具 %q 已注册，不允许重复注册", name))
	}

	params, err := normalizeParameters(spec.Parameters)
	if err != nil {
		return registerRejection(ErrInvalidToolSpec, fmt.Sprintf("工具 %q 的 Parameters 非法: %s", name, err))
	}

	r.tools[name] = t
	r.specs[name] = core.ToolSpec{
		Name:        name,
		Description: spec.Description,
		Parameters:  params,
	}
	return nil
}

// Seal 封闭注册表，使其进入只读阶段；重复调用是幂等的。
//
// 是否在装配完成后调用由装配层负责：未调用时注册表仍允许注册。
func (r *MapRegistry) Seal() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
	return nil
}

// Lookup 按名称取回注册时的同一 Tool 实例；未命中返回 (nil, false)。
func (r *MapRegistry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Specs 返回全部工具元数据的深拷贝，按 Name 字典序排序，保证 LLM 请求可复现。
func (r *MapRegistry) Specs() []core.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]core.ToolSpec, 0, len(r.specs))
	for _, spec := range r.specs {
		out = append(out, cloneSpec(spec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// registerRejection 构造注册拒绝错误：Kind 固定 invalid_argument，Cause 为对应哨兵。
func registerRejection(cause error, detail string) error {
	return &core.RunError{
		Kind:    core.KindInvalidArgument,
		Op:      registerOp,
		Message: detail,
		Cause:   cause,
	}
}

// cloneSpec 复制 Spec 及 Parameters 字节，避免调用方反向修改注册表内定义。
func cloneSpec(spec core.ToolSpec) core.ToolSpec {
	return core.ToolSpec{
		Name:        spec.Name,
		Description: spec.Description,
		Parameters:  append([]byte(nil), spec.Parameters...),
	}
}
