import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import { setBranding, type Appearance, type SpecBranding } from "../api/projects";

const APPEARANCES: { value: "" | Appearance; label: string }[] = [
  { value: "", label: "System default" },
  { value: "system", label: "System" },
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
];

// BrandingEditor edits a project's application branding (DESIGN.md §19): name,
// logo reference, primary accent color and appearance mode. It replaces the
// whole branding on save (an all-empty save clears it to the generated
// defaults), then calls onChanged so the parent reloads.
export function BrandingEditor({
  projectID,
  branding,
  onChanged,
}: {
  projectID: number;
  branding: SpecBranding;
  onChanged: () => void;
}) {
  const [appName, setAppName] = useState(branding.app_name ?? "");
  const [logoRef, setLogoRef] = useState(branding.logo_ref ?? "");
  const [accent, setAccent] = useState(branding.accent_color ?? "");
  const [appearance, setAppearance] = useState<"" | Appearance>(branding.appearance ?? "");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      await setBranding(projectID, {
        app_name: appName,
        logo_ref: logoRef,
        accent_color: accent,
        appearance,
      });
      onChanged();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="branding-editor" aria-label="Branding" onSubmit={onSubmit}>
      <h3>Branding</h3>
      <label className="stacked">
        Application name
        <input aria-label="Application name" value={appName} onChange={(e) => setAppName(e.target.value)} disabled={pending} placeholder="My App" />
      </label>
      <label className="stacked">
        Logo reference
        <input aria-label="Logo reference" value={logoRef} onChange={(e) => setLogoRef(e.target.value)} disabled={pending} placeholder="https://…/logo.svg" />
      </label>
      <div className="branding-row">
        <label className="stacked">
          Accent color
          {/* A native color input keeps the value a valid hex, which the backend
              requires; the text field beside it shows/clears the value. */}
          <span className="accent-controls">
            <input
              type="color"
              aria-label="Accent color"
              value={accent || "#2563eb"}
              onChange={(e) => setAccent(e.target.value)}
              disabled={pending}
            />
            <input
              aria-label="Accent color hex"
              value={accent}
              onChange={(e) => setAccent(e.target.value)}
              disabled={pending}
              placeholder="#2563eb"
            />
          </span>
        </label>
        <label className="stacked">
          Appearance
          <select aria-label="Appearance" value={appearance} onChange={(e) => setAppearance(e.target.value as "" | Appearance)} disabled={pending}>
            {APPEARANCES.map((a) => (
              <option key={a.value} value={a.value}>
                {a.label}
              </option>
            ))}
          </select>
        </label>
      </div>

      <button type="submit" disabled={pending}>
        {pending ? "Saving…" : "Save branding"}
      </button>

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </form>
  );
}
