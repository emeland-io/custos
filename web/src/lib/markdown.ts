// lib/markdown.ts renders a small, safe subset of Markdown for previewing
// a single answer or task body in the browser. Untrusted content is
// HTML-escaped before any tag is introduced, so a task or answer body
// cannot inject markup. This is a readability aid, not a preview of the
// assembled book (that comes from the server's own HTML export, plan 4b).
function escapeHTML(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

function inline(s: string): string {
  let out = escapeHTML(s)
  out = out.replace(/`([^`]+)`/g, '<code>$1</code>')
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>')
  out = out.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>')
  return out
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
