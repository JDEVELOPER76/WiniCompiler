// Stub mínimo de 'vscode' para probar la lógica pura de la extensión.
class Position {
  constructor(line, character) { this.line = line; this.character = character; }
}
class Range {
  constructor(a, b, c, d) {
    if (a instanceof Position) { this.start = a; this.end = b; }
    else { this.start = new Position(a, b); this.end = new Position(c, d); }
  }
}
class CompletionItem {
  constructor(label, kind) { this.label = label; this.kind = kind; }
}
const CompletionItemKind = { Function: 1, Class: 2, Module: 3, Field: 4, Keyword: 5, TypeParameter: 6 };
class CompletionList {
  constructor(items, isIncomplete) { this.items = items; this.isIncomplete = isIncomplete; }
}
class SnippetString { constructor(v) { this.value = v; } }
class MarkdownString {
  constructor() { this.value = ''; }
  appendText(t) { this.value += t; return this; }
  appendMarkdown(t) { this.value += t; return this; }
  appendCodeblock(t) { this.value += t; return this; }
}
const logs = [];
module.exports = {
  Position, Range, CompletionItem, CompletionItemKind, CompletionList,
  SnippetString, MarkdownString,
  workspace: {
    workspaceFolders: [],
    getConfiguration: () => ({ get: (k, d) => d }),
  },
  window: { createOutputChannel: () => ({ appendLine: (m) => logs.push(m) }) },
  __logs: logs,
};
module.exports.SemanticTokensLegend = class { constructor(a, b) { this.tokenTypes = a; this.tokenModifiers = b; } };
module.exports.CodeActionKind = { QuickFix: 'quickfix', SourceOrganizeImports: 'source.organizeImports', Empty: '' };
module.exports.DocumentHighlightKind = { Text: 0, Read: 1, Write: 2 };
module.exports.SymbolKind = { Function: 11, Class: 4, Module: 1 };
module.exports.EndOfLine = { LF: 1, CRLF: 2 };
module.exports.WorkspaceEdit = class { constructor() { this._edits = []; } insert(u, p, t) { this._edits.push({ u, p, t }); } replace(u, r, t) { this._edits.push({ u, r, t }); } };
module.exports.Location = class { constructor(uri, range) { this.uri = uri; this.range = range; } };
module.exports.CodeLens = class { constructor(range, command) { this.range = range; this.command = command; } };
module.exports.DocumentHighlight = class { constructor(range, kind) { this.range = range; this.kind = kind; } };
module.exports.TextEdit = { replace: (range, newText) => ({ range, newText }), insert: (pos, newText) => ({ pos, newText }) };
module.exports.SymbolInformation = class { constructor(name, kind, containerName, location) { this.name = name; this.kind = kind; this.containerName = containerName; this.location = location; } };
module.exports.ThemeColor = class { constructor(id) { this.id = id; } };
module.exports.DiagnosticSeverity = { Error: 0, Warning: 1, Information: 2, Hint: 3 };
module.exports.StatusBarAlignment = { Left: 1, Right: 2 };
module.exports.CompletionTriggerKind = { Invoke: 0, TriggerCharacter: 1, TriggerForIncompleteCompletions: 2 };
module.exports.SemanticTokensBuilder = class {
  constructor(legend) { this.legend = legend; this.tokens = []; }
  push(line, char, len, type, mod) { this.tokens.push({ line, char, len, type, mod }); }
  build() { return { data: this.tokens }; }
};
