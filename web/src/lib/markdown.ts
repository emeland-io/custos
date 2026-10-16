// lib/markdown.ts renders a small, safe subset of Markdown for previewing
// a single answer or task body in the browser. Untrusted content is
// HTML-escaped before any tag is introduced, so a task or answer body
// cannot inject markup. This is a readability aid, not a preview of the
// assembled book (that comes from the server's own HTML export, plan 4b).
function escapeHTML(s: string): string {
  // Strip stray NUL bytes up front: they have no legitimate role in task
  // or answer text, and inline() below uses \u0000 as an internal sentinel
  // for code-span placeholders, so this guarantees that sentinel can never
  // collide with anything that originated in untrusted input.
  return s
    .replace(/\u0000/g, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

// inline() renders the inline forms (code, bold, italic, link) in a single
// left-to-right pass per construct, but code spans are extracted first and
// replaced with opaque \u0000<index>\u0000 placeholders before bold/italic/
// link run. Without this, chained global replaces operate on each other's
// output: a `**not bold**` code span would get re-processed by the bold
// pass, and a link target containing a code span (e.g.
// `[text](http://example.com/\`x\`)`) would fold the resulting <code> tag
// into the href attribute value instead of leaving it as a sibling
// element. Stashing code content behind a placeholder makes it inert to
// every later pass; substituting it back in last restores it verbatim.
function inline(s: string): string {
  const escaped = escapeHTML(s)
  const codeSpans: string[] = []
  let out = escaped.replace(/`([^`]+)`/g, (_match, code: string) => {
    const index = codeSpans.push(`<code>${code}</code>`) - 1
    return `\u0000${index}\u0000`
  })
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>')
  // The URL class excludes the sentinel: a code-span placeholder can never
  // be part of a real URL, so a link target that would otherwise swallow
  // one fails to match here and is left as literal text instead.
  out = out.replace(
    /\[([^\]]+)\]\((https?:\/\/[^\s)\u0000]+)\)/g,
    '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>',
  )
  return out.replace(/\u0000(\d+)\u0000/g, (_match, index: string) => codeSpans[Number(index)])
}

// renderMarkdown understands fenced code blocks, ATX headings (# through
// ######), unordered (-/*) and ordered (1.) lists, blank-line-separated
// paragraphs, and the inline forms above. Anything else renders as a
// plain paragraph with its Markdown syntax still visible, which is honest
// about what this renderer does not understand.
export function renderMarkdown(src: string): string {
  const lines = src.replace(/\r\n/g, '\n').split('\n')
  const html: string[] = []
  let inCode = false
  let codeLines: string[] = []
  let paragraph: string[] = []
  let listItems: string[] = []
  let listTag: 'ul' | 'ol' | null = null

  const flushParagraph = () => {
    if (paragraph.length) {
      html.push('<p>' + paragraph.map(inline).join('<br>') + '</p>')
      paragraph = []
    }
  }
  const flushList = () => {
    if (listTag) {
      html.push(`<${listTag}>` + listItems.map((it) => `<li>${inline(it)}</li>`).join('') + `</${listTag}>`)
      listItems = []
      listTag = null
    }
  }

  for (const line of lines) {
    if (line.trim().startsWith('```')) {
      if (inCode) {
        html.push('<pre><code>' + escapeHTML(codeLines.join('\n')) + '</code></pre>')
        codeLines = []
        inCode = false
      } else {
        flushParagraph()
        flushList()
        inCode = true
      }
      continue
    }
    if (inCode) {
      codeLines.push(line)
      continue
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line)
    if (heading) {
      flushParagraph()
      flushList()
      const level = heading[1].length
      html.push(`<h${level}>${inline(heading[2])}</h${level}>`)
      continue
    }
    const unordered = /^[-*]\s+(.*)$/.exec(line)
    const ordered = /^\d+\.\s+(.*)$/.exec(line)
    if (unordered || ordered) {
      flushParagraph()
      const tag = unordered ? 'ul' : 'ol'
      if (listTag && listTag !== tag) flushList()
      listTag = tag
      listItems.push((unordered ?? ordered)![1])
      continue
    }
    if (line.trim() === '') {
      flushParagraph()
      flushList()
      continue
    }
    flushList()
    paragraph.push(line)
  }
  flushParagraph()
  flushList()
  if (inCode) {
    html.push('<pre><code>' + escapeHTML(codeLines.join('\n')) + '</code></pre>')
  }
  return html.join('\n')
}
