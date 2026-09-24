import type { ReactNode } from "react";
import { DataArea } from "../areas/DataArea";
import { DeployArea } from "../areas/DeployArea";
import { DesignPreview } from "../areas/DesignPreview";
import { PagesArea } from "../areas/PagesArea";

// The four editor areas (DESIGN.md §17). This is the single source of truth for
// navigation and routing, so the shell nav and the router can never disagree
// about which areas exist or their order.
export interface EditorArea {
  path: string;
  label: string;
  // summary describes what the area will hold; areas whose editor is not built
  // yet render their summary as a placeholder.
  summary: string;
  // element is the area's editor, when it exists; areas without one fall back to
  // the summary placeholder (see App).
  element?: ReactNode;
}

export const AREAS: EditorArea[] = [
  { path: "data", label: "Data", summary: "Resource tree and field editor.", element: <DataArea /> },
  { path: "pages", label: "Pages", summary: "Page list and structured page properties.", element: <PagesArea /> },
  { path: "preview", label: "Preview", summary: "Structural design preview rendered from the spec.", element: <DesignPreview /> },
  { path: "access", label: "Access", summary: "Users, groups and permission configuration." },
  {
    path: "deploy",
    label: "Deploy",
    summary: "Preview, build history, production deployment and logs.",
    element: <DeployArea />,
  },
];

export const DEFAULT_AREA = AREAS[0];

// AreaPlaceholder is the interim view for an area whose editor is not built yet.
export function AreaPlaceholder({ area }: { area: EditorArea }): ReactNode {
  return (
    <section aria-labelledby={`area-${area.path}`}>
      <h2 id={`area-${area.path}`}>{area.label}</h2>
      <p>{area.summary}</p>
    </section>
  );
}
