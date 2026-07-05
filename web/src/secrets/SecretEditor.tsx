import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { endpoints, MaskedSecret } from '../lib/endpoints'

const badge: Record<MaskedSecret['origin'], string> = {
  own: 'bg-green-100 text-green-700',
  inherited: 'bg-blue-100 text-blue-700',
  overridden: 'bg-amber-100 text-amber-700',
}

export function SecretEditor() {
  const { configId } = useParams()
  const cid = configId!
  const masked = useQuery({ queryKey: ['config', cid, 'masked'], queryFn: () => endpoints.maskedSecrets(cid) })
  // Revealed plaintext lives ONLY here — never in the query cache; cleared on unmount.
  const [revealed, setRevealed] = useState<Record<string, string>>({})

  async function reveal(key: string) {
    const r = await endpoints.revealKey(cid, key)
    setRevealed((m) => ({ ...m, [key]: r.value }))
  }

  if (masked.isLoading) return <p>Loading…</p>
  if (masked.isError) return <p role="alert">Could not load secrets.</p>
  const rows = Object.entries(masked.data ?? {})

  return (
    <table className="w-full text-sm">
      <thead><tr className="text-left text-gray-400"><th>KEY</th><th>VALUE</th><th>ORIGIN</th><th>v</th></tr></thead>
      <tbody>
        {rows.map(([key, meta]) => (
          <tr key={key} className="border-t">
            <td className="py-1 font-mono">{key}</td>
            <td className="py-1 font-mono">
              {key in revealed ? revealed[key] : '•••••••'}
              {!(key in revealed) && (
                <button onClick={() => void reveal(key)} aria-label={`reveal ${key}`} className="ml-2 text-gray-400">👁</button>
              )}
            </td>
            <td className="py-1"><span className={`rounded px-1.5 ${badge[meta.origin]}`}>{meta.origin}</span></td>
            <td className="py-1 text-gray-400">{meta.value_version}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
