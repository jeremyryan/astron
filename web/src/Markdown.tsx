import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

// Open Markdown links in a new tab (external docs, etc.) rather than
// navigating the app itself away.
const components: Components = {
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noreferrer noopener">
      {children}
    </a>
  ),
};

// Markdown renders GitHub-flavored Markdown as compact, theme-matched HTML.
// Chat answers are explicitly instructed to use Markdown (see
// rag.AnswerMessages and the chat agent's system prompt in
// internal/agent/runner.go), so this is how they're displayed rather than as
// preformatted plain text. Styling lives in styles.css under .md-content;
// only link behavior is overridden here.
export function Markdown({ text }: { text: string }) {
  return (
    <div className="md-content">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
