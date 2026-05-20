# Keyboard shortcuts — FlowState Vue chat

Reference for everything the chat view binds at the document level. Picker
modals (FuzzySearchModal, AgentPicker, ModelPicker) trap Tab inside
themselves; the bindings below operate when no modal is open and the
focus is on the chat thread or input.

## Session navigation

| Combination               | Action                                                                                           | Source                      |
| ------------------------- | ------------------------------------------------------------------------------------------------ | --------------------------- |
| `ArrowUp`                 | Navigate to the parent session of the current child session. No-op on a parent.                  | `useSessionHierarchyNav.ts` |
| `ArrowLeft`               | Navigate to the previous sibling within the current child's parent. Clamps at the first sibling. | `useSessionHierarchyNav.ts` |
| `ArrowRight`              | Navigate to the next sibling within the current child's parent. Clamps at the last sibling.      | `useSessionHierarchyNav.ts` |
| `Ctrl+X` then `ArrowDown` | Load the most-recent delegated child of the current session. The chord times out after 1 second. | `useSessionHierarchyNav.ts` |

The Arrow bindings only fire when the focused element is NOT an editable
input — typing in the composer never accidentally walks the session tree.

## Chat input triggers

| Trigger                                 | Action                                                                                         | Source                 |
| --------------------------------------- | ---------------------------------------------------------------------------------------------- | ---------------------- |
| Type `/` at the start of a word         | Open the slash-command picker. The fragment after `/` is forwarded as the picker's seed query. | `useInputTriggers.ts`  |
| Type `@` at the start of a word         | Open the agent-mention picker. The fragment after `@` is forwarded as the picker's seed query. | `useInputTriggers.ts`  |
| `Esc` while a picker is open            | Close the picker without selecting.                                                            | `FuzzySearchModal.vue` |
| `Enter` inside a picker                 | Select the highlighted item and close.                                                         | `FuzzySearchModal.vue` |
| `ArrowUp` / `ArrowDown` inside a picker | Move the highlight.                                                                            | `FuzzySearchModal.vue` |

## Modal accessibility (Principal F9)

Every picker built on `FuzzySearchModal` traps `Tab` and `Shift+Tab`
inside the modal so keyboard-only users cannot fall out into the
underlying chat thread. The trap is implemented by `useFocusTrap`.
`Escape` is intentionally NOT trapped — the modal owner's existing
Escape handler runs.

## Chat composer

| Combination   | Action                                                            |
| ------------- | ----------------------------------------------------------------- |
| `Enter`       | Send the current message (when not composing IME, no shift held). |
| `Shift+Enter` | Insert a newline without sending.                                 |

(Implementation in `MessageInput.vue`.)
