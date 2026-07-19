/* In-app modal dialogs replacing native confirm()/prompt()/alert().
   Promise-based: `if (!(await dialog.confirm({...}))) return`. */

export interface DialogOpts {
  title: string
  body?: string
  confirmLabel?: string
  danger?: boolean
  /* prompt-only */
  placeholder?: string
  initial?: string
  label?: string
}

interface DialogState extends DialogOpts {
  kind: 'confirm' | 'prompt' | 'notice'
  resolve: (v: boolean | string | null) => void
}

let current = $state<DialogState | null>(null)

export const dialog = {
  get current() { return current },

  confirm(opts: DialogOpts): Promise<boolean> {
    return new Promise(resolve => {
      current = { kind: 'confirm', resolve: v => resolve(v === true), ...opts }
    })
  },

  prompt(opts: DialogOpts): Promise<string | null> {
    return new Promise(resolve => {
      current = { kind: 'prompt', resolve: v => resolve(typeof v === 'string' ? v : null), ...opts }
    })
  },

  notice(opts: DialogOpts): Promise<void> {
    return new Promise(resolve => {
      current = { kind: 'notice', resolve: () => resolve(), ...opts }
    })
  },

  settle(value: boolean | string | null) {
    const d = current
    current = null
    d?.resolve(value)
  },
}
