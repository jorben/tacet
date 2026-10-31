/** Stand-ins for the images embedded in the sample document. */
export function PhotoArt() {
  return (
    <svg viewBox="0 0 104 132" width="104" height="132" aria-hidden="true">
      <rect width="104" height="132" fill="#dce6f2" />
      <circle cx="52" cy="54" r="21" fill="#9fb0c4" />
      <path d="M12 132c4-30 21-43 40-43s36 13 40 43z" fill="#9fb0c4" />
    </svg>
  );
}

export function SealArt() {
  return (
    <svg viewBox="0 0 120 120" width="120" height="120" aria-hidden="true">
      <defs>
        <path id="sa" d="M17,62 a43,43 0 1,1 86,0" />
      </defs>
      <circle cx="60" cy="60" r="55" fill="none" stroke="#c8102e" strokeWidth="3.2" />
      <text fontSize="11.5" fill="#c8102e" fontFamily="Songti SC,SimSun,serif" letterSpacing="1.6">
        <textPath href="#sa" startOffset="50%" textAnchor="middle">
          杭州澄音科技有限公司
        </textPath>
      </text>
      <text x="60" y="73" textAnchor="middle" fontSize="26" fill="#c8102e">
        ★
      </text>
      <text x="60" y="97" textAnchor="middle" fontSize="10" fill="#c8102e" fontFamily="Songti SC,SimSun,serif">
        合同专用章
      </text>
    </svg>
  );
}
