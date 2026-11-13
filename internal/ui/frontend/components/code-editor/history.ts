export interface EditorSnapshot {
  value: string;
  start: number;
  end: number;
  direction: "forward" | "backward" | "none";
  scrollTop: number;
  scrollLeft: number;
}

export type EditKind = "insertText" | "deleteContentBackward" | "deleteContentForward" | "composition" | "atomic";

interface EditGroup {
  kind: EditKind;
  time: number;
}

export function clampSnapshot(snapshot: EditorSnapshot, value = snapshot.value): EditorSnapshot {
  const start = Math.min(Math.max(0, snapshot.start), value.length);
  return { ...snapshot, value, start, end: Math.min(Math.max(start, snapshot.end), value.length) };
}

/** Keep accepted values and their selection in one history. */
export class EditorHistory {
  private entries: EditorSnapshot[];
  private index = 0;
  private group: EditGroup | undefined;

  constructor(initial: EditorSnapshot, private readonly limit = 100) {
    this.entries = [clampSnapshot(initial)];
  }

  get current(): EditorSnapshot {
    return this.entries[this.index];
  }

  peek(direction: -1 | 1): EditorSnapshot | undefined {
    return this.entries[this.index + direction];
  }

  breakGroup(): void {
    this.group = undefined;
  }

  capture(snapshot: EditorSnapshot, preserveGroup = false): void {
    if (snapshot.value !== this.current.value) return;
    const next = clampSnapshot(snapshot);
    if (!preserveGroup && (next.start !== this.current.start || next.end !== this.current.end || next.direction !== this.current.direction)) {
      this.breakGroup();
    }
    this.entries[this.index] = next;
  }

  record(snapshot: EditorSnapshot, kind: EditKind = "atomic", time = Date.now()): void {
    const next = clampSnapshot(snapshot);
    const previous = this.current;
    if (next.value === previous.value) {
      this.capture(next);
      return;
    }
    const grouped = kind !== "atomic" && this.group?.kind === kind
      && (kind === "composition" || time - this.group.time <= 750)
      && (kind === "composition" || (previous.start === previous.end && next.start === next.end))
      && this.index === this.entries.length - 1;
    if (grouped) {
      this.entries[this.index] = next;
    } else {
      this.entries = this.entries.slice(0, this.index + 1);
      this.entries.push(next);
      if (this.entries.length > this.limit) this.entries.shift();
      this.index = this.entries.length - 1;
    }
    this.group = kind === "atomic" ? undefined : { kind, time };
  }

  move(direction: -1 | 1): EditorSnapshot | undefined {
    const next = this.peek(direction);
    this.breakGroup();
    if (next) this.index += direction;
    return next;
  }
}
