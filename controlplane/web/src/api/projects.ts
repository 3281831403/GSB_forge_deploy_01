import { api } from "./client";

// Control-plane resources the editor reads. These mirror the Go wire types
// (contract.Data envelopes); only the fields the editor uses are typed.

export interface Organization {
  id: number;
  name: string;
  slug: string;
}

export interface Project {
  id: number;
  organization_id: number;
  name: string;
  slug: string;
  head_revision_id?: number | null;
}

// One enum value on a field.
export interface EnumValue {
  value: string;
  label?: string;
}

// A field as it appears in a ProjectSpec's resource.
export interface SpecField {
  id: string;
  label: string;
  type: FieldType;
  code_name: string;
  storage_name: string;
  required?: boolean;
  unique?: boolean;
  default?: string | null;
  enum_values?: EnumValue[];
  target?: string; // belongs_to target resource id
}

// A resource's behavior settings (DESIGN.md §4.3).
export interface ResourceBehavior {
  create_enabled?: boolean;
  update_enabled?: boolean;
  delete_enabled?: boolean;
  admin_visible?: boolean;
  list_fields?: string[];
  searchable_fields?: string[];
  sortable_fields?: string[];
  filterable_fields?: string[];
}

// A resource as it appears in a ProjectSpec.
export interface SpecResource {
  id: string;
  label: string;
  label_plural?: string;
  code_name: string;
  storage_name: string;
  fields?: SpecField[];
  behavior?: ResourceBehavior;
}

// The MVP field types the field editor offers. belongs_to is created with the
// relationship editor (#46), so it is not in this list.
export type FieldType =
  | "string"
  | "text"
  | "integer"
  | "decimal"
  | "boolean"
  | "datetime"
  | "date"
  | "enum"
  | "belongs_to";

export const EDITABLE_FIELD_TYPES: FieldType[] = [
  "string",
  "text",
  "integer",
  "decimal",
  "boolean",
  "datetime",
  "date",
  "enum",
];

// The four structured MVP page types (DESIGN.md §4.4). There is deliberately no
// freeform canvas (D6).
export type PageType = "resource_table" | "resource_form" | "resource_detail" | "dashboard";

export const PAGE_TYPES: { type: PageType; label: string; boundToResource: boolean }[] = [
  { type: "resource_table", label: "Resource table", boundToResource: true },
  { type: "resource_form", label: "Resource form", boundToResource: true },
  { type: "resource_detail", label: "Resource detail", boundToResource: true },
  { type: "dashboard", label: "Dashboard", boundToResource: false },
];

// A resource_table page's configuration (DESIGN.md §4.4, §18).
export interface SpecTableConfig {
  title?: string;
  columns?: string[]; // ordered column field ids
  search?: boolean;
  page_size?: number;
}

// A resource_form page's configuration.
export interface SpecFormConfig {
  layout?: string;
  fields?: string[]; // ordered field ids
}

// One dashboard card (count card or recent list) bound to a resource.
export interface SpecDashboardCard {
  label: string;
  resource: string;
  limit?: number;
}

// A dashboard page's configuration.
export interface SpecDashboardConfig {
  count_cards?: SpecDashboardCard[];
  recent_lists?: SpecDashboardCard[];
}

// A page as it appears in a ProjectSpec.
export interface SpecPage {
  id: string;
  slug: string;
  label: string;
  type: PageType;
  resource?: string; // bound resource id; absent for dashboard
  table?: SpecTableConfig; // present only for a resource_table page that configures one
  form?: SpecFormConfig; // present only for a resource_form page that configures one
  dashboard?: SpecDashboardConfig; // present only for a dashboard page
}

// A navigation entry as it appears in a ProjectSpec (DESIGN.md §4.5).
export type NavTarget = "page" | "external";

export interface SpecNavItem {
  id: string;
  label: string;
  target: NavTarget;
  page?: string; // target page id (for a page entry)
  url?: string; // external URL (for an external entry)
}

// Application branding (DESIGN.md §19).
export type Appearance = "light" | "dark" | "system";

export interface SpecBranding {
  app_name?: string;
  logo_ref?: string;
  accent_color?: string;
  appearance?: Appearance;
}

export interface ProjectSpec {
  resources?: SpecResource[] | null;
  pages?: SpecPage[] | null;
  navigation?: SpecNavItem[] | null;
  branding?: SpecBranding | null;
}

// A revision reference returned by a committed edit.
export interface RevisionRef {
  id: number;
  spec_hash: string;
  abi_class?: string;
}

// One dependency that blocks a deletion.
export interface DeletionBlocker {
  kind: string;
  message: string;
}

// The result of a delete: committed, or blocked with the concrete dependencies.
export interface DeleteResult {
  committed: boolean;
  revision_id?: number;
  had_extension: boolean;
  blockers?: DeletionBlocker[];
}

// One of the three independent health facets (spec / ABI / build, §71).
export interface HealthFacet {
  name: string;
  status: "ok" | "failed" | "unknown";
  summary: string;
}

export interface HealthDiagnostic {
  path: string;
  code: string;
  message: string;
  entity?: string;
}

export interface ProjectHealth {
  facets: HealthFacet[];
  diagnostics?: HealthDiagnostic[];
}

export const listOrganizations = () => api.get<Organization[]>("/organizations");

export const getProject = (projectID: number) => api.get<Project>(`/projects/${projectID}`);

export const getProjectHealth = (projectID: number) => api.get<ProjectHealth>(`/projects/${projectID}/health`);

export const addResource = (projectID: number, label: string, labelPlural: string) =>
  api.post<RevisionRef>(`/projects/${projectID}/resources`, { label, label_plural: labelPlural });

export const renameResource = (projectID: number, resourceID: string, label: string, labelPlural: string) =>
  api.patch<RevisionRef>(`/projects/${projectID}/resources/${encodeURIComponent(resourceID)}`, {
    label,
    label_plural: labelPlural,
  });

export const deleteResource = (projectID: number, resourceID: string) =>
  api.delete<DeleteResult>(`/projects/${projectID}/resources/${encodeURIComponent(resourceID)}`);

// FieldInput is what the field editor sends; the backend mints the symbol.
export interface FieldInput {
  label: string;
  type: FieldType;
  required?: boolean;
  unique?: boolean;
  default?: string | null;
  enum_values?: EnumValue[];
}

const fieldPath = (projectID: number, resourceID: string) =>
  `/projects/${projectID}/resources/${encodeURIComponent(resourceID)}/fields`;

export const addField = (projectID: number, resourceID: string, input: FieldInput) =>
  api.post<RevisionRef>(fieldPath(projectID, resourceID), input);

export const updateField = (projectID: number, resourceID: string, fieldID: string, input: FieldInput) =>
  api.patch<RevisionRef>(`${fieldPath(projectID, resourceID)}/${encodeURIComponent(fieldID)}`, input);

export const deleteField = (projectID: number, resourceID: string, fieldID: string) =>
  api.delete<RevisionRef>(`${fieldPath(projectID, resourceID)}/${encodeURIComponent(fieldID)}`);

// RelationshipInput describes a belongs_to relationship to create. The has_many
// side is derived by the compiler from inverse_label.
export interface RelationshipInput {
  label: string;
  target: string;
  inverse_label?: string;
  required?: boolean;
}

export const addRelationship = (projectID: number, resourceID: string, input: RelationshipInput) =>
  api.post<RevisionRef>(`/projects/${projectID}/resources/${encodeURIComponent(resourceID)}/relationships`, input);

// updateBehavior replaces the resource's whole behavior — the client must send
// the complete object, not a delta (omitted settings reset).
export const updateBehavior = (projectID: number, resourceID: string, behavior: ResourceBehavior) =>
  api.patch<RevisionRef>(`/projects/${projectID}/resources/${encodeURIComponent(resourceID)}/behavior`, behavior);

// PageInput is what the page editor sends; the backend mints the id and derives
// the slug. resource is required for every type except dashboard.
export interface PageInput {
  label: string;
  type: PageType;
  resource?: string;
}

export const addPage = (projectID: number, input: PageInput) =>
  api.post<RevisionRef>(`/projects/${projectID}/pages`, input);

export const deletePage = (projectID: number, pageID: string) =>
  api.delete<RevisionRef>(`/projects/${projectID}/pages/${encodeURIComponent(pageID)}`);

// TableConfigInput is what the table-config editor sends. It replaces the whole
// configuration, so the client sends the complete set, not a delta.
export interface TableConfigInput {
  label: string;
  title?: string;
  columns?: string[];
  search?: boolean;
  page_size?: number;
}

export const updateTableConfig = (projectID: number, pageID: string, input: TableConfigInput) =>
  api.patch<RevisionRef>(`/projects/${projectID}/pages/${encodeURIComponent(pageID)}/table`, input);

// The form layouts the editor offers (DESIGN.md §18).
export type FormLayout = "single_column" | "two_column" | "section_groups";

// FormConfigInput is what the form-config editor sends; it replaces the whole
// configuration.
export interface FormConfigInput {
  label: string;
  layout?: "" | FormLayout;
  fields?: string[];
}

export const updateFormConfig = (projectID: number, pageID: string, input: FormConfigInput) =>
  api.patch<RevisionRef>(`/projects/${projectID}/pages/${encodeURIComponent(pageID)}/form`, input);

export const listProjects = (orgID: number) => api.get<Project[]>(`/organizations/${orgID}/projects`);

// NavItemInput is one navigation entry the editor sends. SetNavigation replaces
// the whole ordered list, so the client sends the complete navigation.
export interface NavItemInput {
  label: string;
  target: NavTarget;
  page?: string;
  url?: string;
}

export const setNavigation = (projectID: number, items: NavItemInput[]) =>
  api.put<RevisionRef>(`/projects/${projectID}/navigation`, { items });

// BrandingInput is what the branding editor sends. It replaces the whole
// branding; an all-empty body clears it to the generated defaults.
export interface BrandingInput {
  app_name?: string;
  logo_ref?: string;
  accent_color?: string;
  appearance?: "" | Appearance;
}

export const setBranding = (projectID: number, input: BrandingInput) =>
  api.put<RevisionRef>(`/projects/${projectID}/branding`, input);

// getProjectSpec returns the head revision's spec, or null when the project has
// no revisions yet.
export const getProjectSpec = (projectID: number) => api.get<ProjectSpec | null>(`/projects/${projectID}/spec`);
