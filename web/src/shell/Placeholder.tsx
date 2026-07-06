import { useTitle } from '../lib/title'

export function Placeholder({ feature }: { feature: string }) {
  useTitle(feature)
  return (
    <div className="mt-16 text-center">
      <p className="text-[15px] font-semibold text-muted">{feature}</p>
      <p className="text-[12.5px] text-faint">Coming in a later Phase-2 slice.</p>
    </div>
  )
}
