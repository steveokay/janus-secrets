export function Placeholder({ feature }: { feature: string }) {
  return (
    <div className="mt-16 text-center text-gray-500">
      <p className="text-lg">{feature}</p>
      <p className="text-sm">Coming in a later Phase-2 slice.</p>
    </div>
  )
}
