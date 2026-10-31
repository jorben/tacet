import { createContext, useContext } from "react";

/** In the compare view, hovering one copy of a hit outlines the other one too. */
export const TwinContext = createContext<number | null>(null);

export const useTwin = (hitId: number): boolean => useContext(TwinContext) === hitId;
