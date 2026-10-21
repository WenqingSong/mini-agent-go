package tool

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TestMapRegistryRegisterLookupAndSpecs 覆盖 AC-001：注册后查找命中同一实例，
// Specs() 输出可被 llm.Request.Tools 直接使用的元数据。
func TestMapRegistryRegisterLookupAndSpecs(t *testing.T) {
	const params = `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`
	weather := newFakeTool("get_weather").withParameters(params)
	weather.description = "查询城市天气"

	registry := NewMapRegistry()
	if err := registry.Register(weather); err != nil {
		t.Fatalf("注册合法工具不应失败：%v", err)
	}

	found, ok := registry.Lookup("get_weather")
	if !ok {
		t.Fatal("Lookup 应命中已注册工具")
	}
	if found != Tool(weather) {
		t.Fatal("Lookup 必须返回注册时传入的同一 Tool 实例")
	}

	specs := registry.Specs()
	if len(specs) != 1 {
		t.Fatalf("Specs 应返回 1 个工具，实际 %d", len(specs))
	}
	if specs[0].Name != "get_weather" {
		t.Fatalf("Name 不符：%q", specs[0].Name)
	}
	if specs[0].Description != "查询城市天气" {
		t.Fatalf("Description 不符：%q", specs[0].Description)
	}
	if string(specs[0].Parameters) != params {
		t.Fatalf("Parameters 必须逐字节保留，实际 %s", specs[0].Parameters)
	}

	// 名称大小写敏感且不 trim：不做归一化匹配。
	if _, ok := registry.Lookup("GET_WEATHER"); ok {
		t.Fatal("Lookup 不应做大小写归一化")
	}
	if _, ok := registry.Lookup("get_weather "); ok {
		t.Fatal("Lookup 不应做空白归一化")
	}
}

// TestMapRegistryLookupMissing 覆盖未命中语义：返回 (nil, false)，不 panic。
func TestMapRegistryLookupMissing(t *testing.T) {
	registry := NewMapRegistry()

	tool, ok := registry.Lookup("missing")
	if ok {
		t.Fatal("未注册的名称不应命中")
	}
	if tool != nil {
		t.Fatalf("未命中时应返回 nil Tool，实际 %v", tool)
	}
}

// TestMapRegistryRejectsInvalidRegistration 覆盖注册期全部拒绝路径（INV-102）。
func TestMapRegistryRejectsInvalidRegistration(t *testing.T) {
	validTool := func() *fakeTool { return newFakeTool("get_weather") }

	cases := []struct {
		name     string
		setup    func(t *testing.T) *MapRegistry
		register Tool
		wantErr  error
	}{
		{
			name:     "名称为空",
			register: newFakeTool(""),
			wantErr:  ErrEmptyToolName,
		},
		{
			name:     "名称全空白",
			register: newFakeTool("   "),
			wantErr:  ErrEmptyToolName,
		},
		{
			name: "重复注册同名工具",
			setup: func(t *testing.T) *MapRegistry {
				registry := NewMapRegistry()
				if err := registry.Register(validTool()); err != nil {
					t.Fatalf("首次注册不应失败：%v", err)
				}
				return registry
			},
			register: validTool(),
			wantErr:  ErrDuplicateTool,
		},
		{
			name: "Seal 之后注册",
			setup: func(t *testing.T) *MapRegistry {
				registry := NewMapRegistry()
				if err := registry.Seal(); err != nil {
					t.Fatalf("Seal 不应失败：%v", err)
				}
				return registry
			},
			register: validTool(),
			wantErr:  ErrRegistrySealed,
		},
		{
			name:     "Schema 含未支持关键字",
			register: newFakeTool("city").withParameters(`{"type":"object","properties":{"name":{"type":"string","pattern":"^a$"}}}`),
			wantErr:  ErrInvalidToolSpec,
		},
		{
			name:     "Parameters 非法 JSON",
			register: newFakeTool("broken").withParameters(`{"type":`),
			wantErr:  ErrInvalidToolSpec,
		},
		{
			name:     "根 Schema 不是 object",
			register: newFakeTool("flat").withParameters(`{"type":"string"}`),
			wantErr:  ErrInvalidToolSpec,
		},
		{
			name:     "工具为 nil",
			register: nil,
			wantErr:  ErrInvalidToolSpec,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewMapRegistry()
			if tc.setup != nil {
				registry = tc.setup(t)
			}

			err := registry.Register(tc.register)
			if err == nil {
				t.Fatal("非法注册应返回错误")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("错误应可用 errors.Is 匹配 %v，实际 %v", tc.wantErr, err)
			}
			expectErrorKind(t, err, core.KindInvalidArgument)

			if fake, ok := tc.register.(*fakeTool); ok && fake != nil {
				name := fake.Spec().Name
				if found, ok := registry.Lookup(name); ok && found == Tool(fake) {
					t.Fatalf("注册失败的 %q 实例不应进入注册表", name)
				}
				if got := fake.callCount(); got != 0 {
					t.Fatalf("注册失败不应调用工具实现，实际调用 %d 次", got)
				}
			}
		})
	}
}

// TestMapRegistrySpecsIsDeepCopy 覆盖 INV-107：外部修改返回值或工具自身 Spec 不影响注册表定义。
func TestMapRegistrySpecsIsDeepCopy(t *testing.T) {
	const params = `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`

	alpha := newFakeTool("alpha").withParameters(params)
	zulu := newFakeTool("zulu")

	registry := NewMapRegistry()
	// 故意按非字典序注册，验证 Specs() 输出顺序稳定。
	if err := registry.Register(zulu); err != nil {
		t.Fatalf("注册 zulu 失败：%v", err)
	}
	if err := registry.Register(alpha); err != nil {
		t.Fatalf("注册 alpha 失败：%v", err)
	}

	specs := registry.Specs()
	if len(specs) != 2 || specs[0].Name != "alpha" || specs[1].Name != "zulu" {
		t.Fatalf("Specs 应按名称字典序输出，实际 %+v", specs)
	}
	if string(specs[0].Parameters) != params {
		t.Fatalf("Parameters 不符：%s", specs[0].Parameters)
	}

	// 篡改返回值不应影响注册表内部定义。
	specs[0].Name = "mutated"
	specs[0].Description = "mutated"
	specs[0].Parameters[0] = 'x'

	specs = registry.Specs()
	if specs[0].Name != "alpha" || specs[0].Description != alpha.description {
		t.Fatalf("修改 Specs 返回值污染了注册表定义：%+v", specs[0])
	}
	if string(specs[0].Parameters) != params {
		t.Fatalf("修改 Specs 返回的 Parameters 字节污染了注册表定义：%s", specs[0].Parameters)
	}

	// 工具自身 Spec() 在注册后变化，也不影响注册表保存的定义。
	alpha.withParameters(`{"type":"object","properties":{"city":{"type":"number"}}}`)
	specs = registry.Specs()
	if string(specs[0].Parameters) != params {
		t.Fatalf("工具 Spec 变化污染了注册表定义：%s", specs[0].Parameters)
	}
}

// TestMapRegistryNormalizesEmptyParameters 覆盖空 Parameters 的规范化。
func TestMapRegistryNormalizesEmptyParameters(t *testing.T) {
	cases := []struct {
		name   string
		params string
	}{
		{name: "nil Parameters", params: ""},
		{name: "全空白 Parameters", params: "   \n\t"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewMapRegistry()
			if err := registry.Register(newFakeTool("empty").withParameters(tc.params)); err != nil {
				t.Fatalf("空 Parameters 应被接受：%v", err)
			}
			specs := registry.Specs()
			if len(specs) != 1 {
				t.Fatalf("Specs 应返回 1 项，实际 %d", len(specs))
			}
			if got := string(specs[0].Parameters); got != `{"type":"object","properties":{}}` {
				t.Fatalf("空 Parameters 应规范化为空对象 Schema，实际 %s", got)
			}
		})
	}
}

// TestMapRegistrySeal 覆盖 Seal 的幂等性与只读语义。
func TestMapRegistrySeal(t *testing.T) {
	registry := NewMapRegistry()
	if err := registry.Register(newFakeTool("echo")); err != nil {
		t.Fatalf("注册失败：%v", err)
	}

	if err := registry.Seal(); err != nil {
		t.Fatalf("Seal 不应失败：%v", err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatalf("重复 Seal 应幂等成功，实际 %v", err)
	}

	// Seal 之后查找与导出不受影响。
	if _, ok := registry.Lookup("echo"); !ok {
		t.Fatal("Seal 之后 Lookup 应正常工作")
	}
	if len(registry.Specs()) != 1 {
		t.Fatalf("Seal 之后 Specs 应正常工作，实际 %d 项", len(registry.Specs()))
	}

	err := registry.Register(newFakeTool("late"))
	if !errors.Is(err, ErrRegistrySealed) {
		t.Fatalf("Seal 之后注册应返回 ErrRegistrySealed，实际 %v", err)
	}
}

// TestMapRegistryConcurrentAccess 覆盖并发安全：Register / Lookup / Specs / Seal 可被多 goroutine 同时调用。
func TestMapRegistryConcurrentAccess(t *testing.T) {
	const (
		workers     = 8
		perWorker   = 20
		totalTools  = workers * perWorker
		extraSealed = 8
	)

	registry := NewMapRegistry()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("tool-%02d-%02d", w, i)
				if err := registry.Register(newFakeTool(name)); err != nil {
					t.Errorf("并发注册 %q 失败：%v", name, err)
					return
				}
				registry.Lookup(name)
				registry.Specs()
			}
		}(w)
	}
	wg.Wait()

	if got := len(registry.Specs()); got != totalTools {
		t.Fatalf("并发注册后应有 %d 个工具，实际 %d", totalTools, got)
	}

	if err := registry.Seal(); err != nil {
		t.Fatalf("Seal 失败：%v", err)
	}

	var sealedWG sync.WaitGroup
	for w := 0; w < extraSealed; w++ {
		sealedWG.Add(1)
		go func(w int) {
			defer sealedWG.Done()
			err := registry.Register(newFakeTool(fmt.Sprintf("sealed-%02d", w)))
			if !errors.Is(err, ErrRegistrySealed) {
				t.Errorf("Seal 之后并发注册应返回 ErrRegistrySealed，实际 %v", err)
			}
			registry.Specs()
			registry.Lookup("tool-00-00")
		}(w)
	}
	sealedWG.Wait()
}

// TestMapRegistrySpecsNeverEmptyForRegisteredNames 保证 Specs 输出与注册表内容一一对应。
func TestMapRegistrySpecsNeverEmptyForRegisteredNames(t *testing.T) {
	registry := NewMapRegistry()
	names := []string{"c", "a", "b"}
	for _, name := range names {
		if err := registry.Register(newFakeTool(name)); err != nil {
			t.Fatalf("注册 %q 失败：%v", name, err)
		}
	}

	specs := registry.Specs()
	if len(specs) != len(names) {
		t.Fatalf("Specs 数量不符：期望 %d，实际 %d", len(names), len(specs))
	}
	var got []string
	for _, spec := range specs {
		got = append(got, spec.Name)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("Specs 顺序应稳定为 a,b,c，实际 %v", got)
	}
}
