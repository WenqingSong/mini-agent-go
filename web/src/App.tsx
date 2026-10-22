import { useEffect, useRef, useState } from 'react'
import type { ButtonHTMLAttributes } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as AlertDialog from '@radix-ui/react-alert-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Tooltip from '@radix-ui/react-tooltip'
import { ArrowUp, Check, Code2, Copy, Ellipsis, Menu, MessageCircle,
  Monitor, Moon, PanelLeft, Plus, Sparkles, Square, Sun, Trash2, X, Calculator, CircleAlert } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { APIError, createSession, sendMessage } from './api'
import { loadArchive, loadTheme, newChat, saveArchive, THEME_KEY, titleFrom } from './model'
import type { Archive, Chat, Message, Theme } from './model'

function Logo({ small = false }: { small?: boolean }) {
  return <span className={`logo ${small ? 'logo-small' : ''}`} aria-hidden="true">
    <svg viewBox="0 0 48 48" fill="none"><path d="M12 33V15l12 11 12-11v18M24 26v11" stroke="currentColor" strokeWidth="3.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
  </span>
}

function IconButton({ label, children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return <Tooltip.Root><Tooltip.Trigger asChild>
    <button {...props} aria-label={label} className={`icon-button ${props.className || ''}`}>{children}</button>
  </Tooltip.Trigger><Tooltip.Portal><Tooltip.Content className="tooltip" sideOffset={7}>{label}</Tooltip.Content></Tooltip.Portal></Tooltip.Root>
}

function CopyButton({ text }: { text: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  useEffect(() => {
    if (state === 'idle') return
    const timeout = setTimeout(() => setState('idle'), 2200)
    return () => clearTimeout(timeout)
  }, [state])
  return <span className="copy-action"><IconButton label={state === 'copied' ? '已复制' : '复制回复'} onClick={async () => {
    try { await navigator.clipboard.writeText(text); setState('copied') } catch { setState('failed') }
  }}>{state === 'copied' ? <Check size={15} /> : <Copy size={15} />}</IconButton>
    {state === 'failed' && <span role="status">无法复制，请手动选择文字</span>}</span>
}

function ThemeMenu({ theme, onTheme }: { theme: Theme; onTheme: (value: Theme) => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild>
    <button className="icon-button" aria-label="选择外观" title="选择外观"><Sun className="light-icon" size={19} /><Moon className="dark-icon" size={19} /></button>
  </DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="menu glass" align="end" sideOffset={8}>
    <DropdownMenu.Label className="menu-label">外观</DropdownMenu.Label>
    <DropdownMenu.RadioGroup value={theme} onValueChange={value => onTheme(value as Theme)}>
      {([{ value: 'light', label: '浅色', icon: Sun }, { value: 'dark', label: '深色', icon: Moon }, { value: 'system', label: '跟随系统', icon: Monitor }] as const).map(({ value, label, icon: Icon }) =>
        <DropdownMenu.RadioItem key={value} value={value} className="menu-item"><Icon size={16} />{label}<DropdownMenu.ItemIndicator className="menu-check"><Check size={15} /></DropdownMenu.ItemIndicator></DropdownMenu.RadioItem>)}
    </DropdownMenu.RadioGroup>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function MessageView({ message }: { message: Message }) {
  if (message.role === 'user') return <div className="user-row"><div className="user-message">{message.content}</div></div>
  return <article className="assistant-row" aria-label="Mini Agent 的回复">
    <div className="assistant-heading"><Logo small /><span>Mini Agent</span></div>
    {message.state === 'pending' ? <div className="thinking" role="status"><span className="thinking-dot" />正在思考<span className="thinking-dots">…</span></div>
      : message.state === 'error' ? <div className="message-error" role="alert"><CircleAlert size={17} /><span>{message.content}</span></div>
      : <><div className="markdown"><Markdown remarkPlugins={[remarkGfm]} disallowedElements={['img']} components={{
        a: ({ children, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer">{children}</a>,
      }}>{message.content || '（模型返回了空回复）'}</Markdown></div>
        <div className="message-actions"><CopyButton text={message.content} />
          {!!message.toolCalls && <span className="tool-badge"><Calculator size={12} />使用了 {message.toolCalls} 次工具</span>}
        </div></>}
  </article>
}

export default function App() {
  const [initial] = useState(loadArchive)
  const [archive, setArchive] = useState(initial.archive)
  const archiveRef = useRef(archive)
  const [storageWarning, setStorageWarning] = useState(initial.warning)
  const [theme, setTheme] = useState(loadTheme)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [deleteId, setDeleteId] = useState<string | null>(null)
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [connection, setConnection] = useState<'checking' | 'ready' | 'offline'>('checking')
  const requests = useRef(new Map<string, AbortController>())
  const textarea = useRef<HTMLTextAreaElement>(null)
  const scroller = useRef<HTMLDivElement>(null)
  const active = archive.chats.find(chat => chat.id === archive.activeId)
  const draftKey = active?.id || 'new'
  const draft = drafts[draftKey] || ''
  const pending = active?.messages.some(m => m.state === 'pending') || false
  const tooLong = Array.from(draft.trim()).length > 8192

  function change(update: (value: Archive) => Archive) {
    const next = update(archiveRef.current)
    archiveRef.current = next
    setArchive(next)
    setStorageWarning(saveArchive(next))
  }
  function patchChat(id: string, update: (chat: Chat) => Chat) {
    change(value => ({ ...value, chats: value.chats.map(chat => chat.id === id ? update(chat) : chat) }))
  }
  function selectChat(id: string | null) {
    change(value => ({ ...value, activeId: id }))
    setMobileOpen(false)
    requestAnimationFrame(() => textarea.current?.focus())
  }
  async function checkConnection(signal?: AbortSignal) {
    setConnection('checking')
    try {
      const result = await fetch('/api/healthz', { signal })
      if (!signal?.aborted) setConnection(result.ok ? 'ready' : 'offline')
    } catch { if (!signal?.aborted) setConnection('offline') }
  }
  useEffect(() => {
    const controller = new AbortController()
    void checkConnection(controller.signal)
    return () => { controller.abort(); requests.current.forEach(request => request.abort()) }
  }, [])
  useEffect(() => {
    const media = matchMedia('(prefers-color-scheme: dark)')
    const apply = () => { document.documentElement.dataset.theme = theme === 'system' ? (media.matches ? 'dark' : 'light') : theme }
    apply()
    media.addEventListener('change', apply)
    return () => media.removeEventListener('change', apply)
  }, [theme])
  useEffect(() => {
    if (textarea.current) {
      textarea.current.style.height = 'auto'
      textarea.current.style.height = `${Math.min(textarea.current.scrollHeight, 180)}px`
    }
  }, [draft, archive.activeId])
  useEffect(() => {
    scroller.current?.scrollTo({ top: scroller.current.scrollHeight, behavior: 'instant' })
  }, [archive.activeId, active?.messages])

  async function submit(text = draft) {
    const content = text.trim()
    const current = archiveRef.current.chats.find(chat => chat.id === archiveRef.current.activeId)
    if (!content || Array.from(content).length > 8192 || current?.unavailable || (current && requests.current.has(current.id))) return
    const chat = current || newChat()
    const controller = new AbortController()
    requests.current.set(chat.id, controller)
    const assistantId = crypto.randomUUID()
    const messages: Message[] = [
      { id: crypto.randomUUID(), role: 'user', content, state: 'done' },
      { id: assistantId, role: 'assistant', content: '', state: 'pending' },
    ]
    const updated = { ...chat, title: chat.messages.length ? chat.title : titleFrom(content), updatedAt: Date.now(), messages: [...chat.messages, ...messages] }
    change(value => ({ ...value, activeId: chat.id, chats: current ? value.chats.map(c => c.id === chat.id ? updated : c) : [updated, ...value.chats] }))
    setDrafts(value => ({ ...value, [current?.id || 'new']: '' }))
    try {
      const sessionId = chat.sessionId || await createSession(controller.signal)
      patchChat(chat.id, value => ({ ...value, sessionId }))
      const reply = await sendMessage(sessionId, content, controller.signal)
      patchChat(chat.id, value => ({ ...value, updatedAt: Date.now(), messages: value.messages.map(m => m.id === assistantId
        ? { ...m, content: reply.reply, state: 'done', llmCalls: reply.llm_calls, toolCalls: reply.tool_calls } : m) }))
      setConnection('ready')
    } catch (error) {
      const code = error instanceof APIError ? error.code : 'network_error'
      const expired = code === 'session_not_found' || code === 'session_expired'
      const uncertain = controller.signal.aborted || ['network_error', 'invalid_response', 'backend_unavailable'].includes(code)
      patchChat(chat.id, value => ({ ...value,
        unavailable: expired ? 'expired' : uncertain ? 'interrupted' : undefined,
        messages: value.messages.map(m => m.id === assistantId ? { ...m, state: 'error', content: controller.signal.aborted
          ? '已停止等待回复。' : error instanceof APIError ? error.message : '连接中断，请稍后再试。' } : m),
      }))
      if (!controller.signal.aborted && (code === 'network_error' || code === 'backend_unavailable')) setConnection('offline')
    } finally { requests.current.delete(chat.id) }
  }

  function sidebar() {
    return <>
      <div className="sidebar-top"><a className="brand" href="/" onClick={event => { event.preventDefault(); selectChat(null) }}><Logo small /><span>Mini Agent</span></a>
        <IconButton label="收起侧栏" className="desktop-only" onClick={() => setSidebarOpen(false)}><PanelLeft size={19} /></IconButton>
        <IconButton label="关闭侧栏" className="mobile-only" onClick={() => setMobileOpen(false)}><X size={19} /></IconButton>
      </div>
      <button className="new-chat glass" onClick={() => selectChat(null)}><Plus size={19} /><span>新对话</span><span className="new-chat-mark">＋</span></button>
      <div className="history-label">最近对话</div>
      <nav className="history" aria-label="对话记录">
        {archive.chats.length === 0 ? <div className="history-empty"><MessageCircle size={20} /><p>你的对话，会留在这里</p></div>
          : [...archive.chats].sort((a, b) => b.updatedAt - a.updatedAt).map(chat => <div className={`history-item ${active?.id === chat.id ? 'active' : ''}`} key={chat.id}>
            <button className="history-link" aria-current={active?.id === chat.id ? 'page' : undefined} onClick={() => selectChat(chat.id)}><span>{chat.title}</span></button>
            <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="history-more icon-button" aria-label={`${chat.title} 的选项`}><Ellipsis size={17} /></button></DropdownMenu.Trigger>
              <DropdownMenu.Portal><DropdownMenu.Content className="menu glass" sideOffset={4} align="start"><DropdownMenu.Item className="menu-item danger" onSelect={() => setDeleteId(chat.id)}><Trash2 size={15} />删除对话</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal>
            </DropdownMenu.Root>
          </div>)}
      </nav>
      <div className="sidebar-footer"><span className="local-dot" /><div>保存在此浏览器<span>你的对话记录仅存储在本地</span></div></div>
    </>
  }

  return <Tooltip.Provider delayDuration={350}>
    <div className={`app ${sidebarOpen ? '' : 'sidebar-collapsed'}`}>
      <aside className="sidebar desktop-only">{sidebar()}</aside>
      <Dialog.Root open={mobileOpen} onOpenChange={setMobileOpen}><Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" /><Dialog.Content className="mobile-sidebar sidebar">
          <Dialog.Title className="sr-only">对话列表</Dialog.Title><Dialog.Description className="sr-only">选择、新建或删除本地对话。</Dialog.Description>{sidebar()}
        </Dialog.Content>
      </Dialog.Portal></Dialog.Root>
      <main className="main">
        <header className="topbar">
          <div className="topbar-left"><IconButton label="展开侧栏" className={`desktop-only ${sidebarOpen ? 'hidden' : ''}`} onClick={() => setSidebarOpen(true)}><PanelLeft size={20} /></IconButton>
            <IconButton label="打开对话列表" className="mobile-only" onClick={() => setMobileOpen(true)}><Menu size={21} /></IconButton>
            <span className="product-name">Mini Agent <span className="version-pill">Go</span></span>
          </div>
          <div className="topbar-right"><button className={`connection ${connection}`} onClick={() => void checkConnection()} aria-label="检查服务连接" title="仅检查服务连通性，模型可用性以对话结果为准"><span />{connection === 'ready' ? '服务已连接' : connection === 'checking' ? '连接中' : '服务未连接'}</button>
            <ThemeMenu theme={theme} onTheme={value => { setTheme(value); try { localStorage.setItem(THEME_KEY, value) } catch { setStorageWarning('外观偏好暂时无法保存。') } }} />
          </div>
        </header>
        {storageWarning && <div className="storage-warning" role="alert">{storageWarning}</div>}
        <div className={`chat-area ${active ? 'has-messages' : 'is-empty'}`}>
          {active ? <div className="messages-scroll" ref={scroller}><div className="messages" role="log" aria-label="聊天消息" aria-live="polite">
            {active.messages.map(message => <MessageView key={message.id} message={message} />)}
          </div></div> : <div className="welcome"><div className="welcome-mark"><Logo /></div><p className="eyebrow">一个问题，一点新想法。</p><h1>有什么可以帮忙的？</h1></div>}
          <div className="composer-zone">
            {active?.unavailable ? <div className="expired-notice glass"><CircleAlert size={18} /><div>{active.unavailable === 'expired' ? '服务会话已过期' : '本次会话的状态无法确认'}<p>本地记录仍可查看。新建对话后，模型将从新的上下文开始。</p></div><button className="text-button" onClick={() => selectChat(null)}>新建对话</button></div>
              : <form className="composer glass" onSubmit={event => { event.preventDefault(); void submit() }}>
                <textarea ref={textarea} aria-label="消息" placeholder="向 Mini Agent 发送消息" rows={1} value={draft} disabled={pending}
                  onChange={event => setDrafts(value => ({ ...value, [draftKey]: event.target.value }))}
                  onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); void submit() } }} />
                <div className="composer-bottom"><span className={`composer-hint ${tooLong ? 'danger' : ''}`}><Sparkles size={14} />{tooLong ? '消息请控制在 8192 字符以内' : '从一个想法开始'}</span>
                  {pending ? <button type="button" className="send-button" aria-label="停止回复" onClick={() => requests.current.get(active!.id)?.abort()}><Square size={15} fill="currentColor" /></button>
                    : <button type="submit" className="send-button" aria-label="发送消息" disabled={!draft.trim() || tooLong}><ArrowUp size={21} strokeWidth={2.2} /></button>}
                </div>
              </form>}
            {!active && <div className="suggestions">
              {[{ icon: Calculator, label: '试试计算器', prompt: '请使用计算工具计算 37 × 48。' }, { icon: Code2, label: '聊聊 Go', prompt: '用一个简单例子解释 Go 的 goroutine。' }, { icon: MessageCircle, label: '整理一个想法', prompt: '我想整理一个学习计划。请先问我一个问题，帮助我明确目标。' }].map(({ icon: Icon, label, prompt }) => <button key={label} onClick={() => { setDrafts(value => ({ ...value, new: prompt })); textarea.current?.focus() }}><Icon size={16} />{label}</button>)}
            </div>}
            <p className="footnote">{active ? '回答由 AI 生成，请核实重要信息。' : '支持连续对话与计算工具。记录保存在当前浏览器。'}<span className="keyboard-hint">Enter 发送 · Shift + Enter 换行</span></p>
          </div>
        </div>
      </main>
      <AlertDialog.Root open={!!deleteId} onOpenChange={open => { if (!open) setDeleteId(null) }}><AlertDialog.Portal>
        <AlertDialog.Overlay className="dialog-overlay" /><AlertDialog.Content className="confirm-dialog glass">
          <AlertDialog.Title>删除这段对话？</AlertDialog.Title><AlertDialog.Description>此操作会删除当前浏览器中的这段记录，无法恢复。</AlertDialog.Description>
          <div className="dialog-actions"><AlertDialog.Cancel asChild><button className="secondary-button">取消</button></AlertDialog.Cancel><AlertDialog.Action asChild><button className="danger-button" onClick={() => {
            if (!deleteId) return
            requests.current.get(deleteId)?.abort()
            change(value => ({ ...value, activeId: value.activeId === deleteId ? null : value.activeId, chats: value.chats.filter(chat => chat.id !== deleteId) }))
          }}>删除对话</button></AlertDialog.Action></div>
        </AlertDialog.Content>
      </AlertDialog.Portal></AlertDialog.Root>
    </div>
  </Tooltip.Provider>
}
