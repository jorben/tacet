import type { CSSProperties } from "react";
import logoUrl from "../assets/logo.webp";
import { ICONS } from "./iconPaths";

interface IconProps {
  name: string;
  size?: number;
  strokeWidth?: number;
  className?: string;
}

/** A stroke icon. The path data comes from the fixed table in `iconPaths`, never from user input. */
export function Icon({ name, size = 16, strokeWidth = 1.75, className }: IconProps) {
  return (
    <svg
      className={className ? "i " + className : "i"}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      dangerouslySetInnerHTML={{ __html: ICONS[name] || "" }}
    />
  );
}

export const LOGO_URL: string = logoUrl;

const LOGO_STYLE: CSSProperties = { display: "block", flex: "none", objectFit: "contain", borderRadius: "22%" };

/** The product mark at a small size (title bar, empty states). */
export function Logo({ size = 18 }: { size?: number }) {
  return <img src={LOGO_URL} width={size} height={size} alt="" aria-hidden="true" style={LOGO_STYLE} />;
}
