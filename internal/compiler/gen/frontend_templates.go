package gen

import (
	"encoding/json"
	"text/template"
)

// frontendFuncs escapes untrusted spec strings (labels, enum values) for the
// JS/JSX context. json.Marshal of a string yields a valid JavaScript string
// literal — quoted, with ", \, control characters and <, >, & all escaped — so
// a label like `Items {n}` or `Day "start"` becomes an inert string rather than
// a JSX expression or a broken literal. Identifiers derived from validated
// code/storage names (Type, RouteBase, …) are already safe and are not escaped.
var frontendFuncs = template.FuncMap{
	"js": func(s string) string {
		out, err := json.Marshal(s)
		if err != nil {
			// A Go string always marshals; fall back to an empty literal rather
			// than emitting something unquoted.
			return `""`
		}
		return string(out)
	},
}

func mustTemplate(name, src string) *template.Template {
	return template.Must(template.New(name).Funcs(frontendFuncs).Parse(src))
}

var (
	tablePageTemplate     = mustTemplate("tablepage", tablePageSrc)
	detailPageTemplate    = mustTemplate("detailpage", detailPageSrc)
	formPageTemplate      = mustTemplate("formpage", formPageSrc)
	dashboardPageTemplate = mustTemplate("dashboardpage", dashboardPageSrc)
	registryTemplate      = mustTemplate("registry", registrySrc)
)

// dashboardPageSrc renders a dashboard (DESIGN.md §4.4): count cards showing a
// real total per resource (read from the list handler's PageMeta, so no Gombit
// change is needed), aggregate cards showing a server-side sum/avg/min/max of a
// numeric field (read from the list handler's ListMeta.aggregates, gombit #273,
// #182), and recent-record lists. A recent list that declares an OrderBy (a
// sortable date/datetime field, #54) fetches the newest records
// (?ordering=-<field>&per_page=<limit>, gombit #260) and renders them; one
// without an OrderBy stays a labeled section with a "View all" link rather than
// fabricating an order. There is no chart designer (§30 non-goal).
const dashboardPageSrc = `{{.Banner}}
{{if .HasFetches}}import { useEffect, useState } from "react";
{{end}}{{if .HasLinks}}import { Link } from "react-router";
{{end}}{{if .HasFetches}}import { useApiClient } from "../../api/client";
import { unwrap } from "../../api/generated/client";
{{end}}{{if .HasRecords}}import type { paths } from "../../api/generated/schema";
{{end}}
{{- range .RecentLists}}{{if .Records}}
type Recent{{.Index}}Row = NonNullable<
  paths["{{.CollectionPath}}"]["get"]["responses"][200]["content"]["application/json"]["data"]
>[number];
{{- end}}{{end}}

export function {{.Component}}() {
{{- if .HasFetches}}
  const client = useApiClient();
{{- end}}
{{- if .CountCards}}
  const [counts, setCounts] = useState<(number | null)[]>([{{range .CountCards}}null, {{end}}]);
{{- end}}
{{- if .AggregateCards}}
  const [aggregates, setAggregates] = useState<(string | null)[]>([{{range .AggregateCards}}null, {{end}}]);
{{- end}}
{{- range .RecentLists}}{{if .Records}}
  const [recent{{.Index}}, setRecent{{.Index}}] = useState<Recent{{.Index}}Row[]>([]);
{{- end}}{{end}}
{{- if .CountCards}}

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const next: (number | null)[] = [];
{{- range .CountCards}}
      try {
        const listed = await unwrap(await client.GET("{{.CollectionPath}}", { params: { query: { per_page: 1 } } }));
        next.push(listed.meta?.total ?? 0);
      } catch {
        next.push(null);
      }
{{- end}}
      if (!cancelled) {
        setCounts(next);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client]);
{{- end}}
{{- if .AggregateCards}}

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const next: (string | null)[] = [];
{{- range .AggregateCards}}
      try {
        const listed = await unwrap(await client.GET("{{.CollectionPath}}", { params: { query: { aggregate: "{{.AggKey}}", per_page: 1 } } }));
        next.push(listed.meta?.aggregates?.["{{.AggKey}}"] ?? "0");
      } catch {
        next.push(null);
      }
{{- end}}
      if (!cancelled) {
        setAggregates(next);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client]);
{{- end}}
{{- range .RecentLists}}{{if .Records}}

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const listed = await unwrap(
          await client.GET("{{.CollectionPath}}", { params: { query: { ordering: "{{.OrderParam}}", per_page: {{.Limit}} } } }),
        );
        if (!cancelled) {
          setRecent{{.Index}}(Array.isArray(listed.data) ? listed.data : []);
        }
      } catch {
        // A failed recent-list fetch leaves the section empty.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client]);
{{- end}}{{end}}

  return (
    <section>
      <h1>{ {{js .Title}} }</h1>
{{- if .CountCards}}
      <div className="count-cards">
{{- range $i, $c := .CountCards}}
        <div className="count-card">
          <span className="count-card-label">{ {{js $c.Label}} }</span>
          <strong className="count-card-value">{counts[{{$i}}] ?? "…"}</strong>
        </div>
{{- end}}
      </div>
{{- end}}
{{- if .AggregateCards}}
      <div className="aggregate-cards">
{{- range $i, $c := .AggregateCards}}
        <div className="aggregate-card">
          <span className="aggregate-card-label">{ {{js $c.Label}} }</span>
          <strong className="aggregate-card-value">{aggregates[{{$i}}] ?? "…"}</strong>
        </div>
{{- end}}
      </div>
{{- end}}
{{- range .RecentLists}}
      <section aria-label={ {{js .Label}} }>
        <h2>{ {{js .Label}} }</h2>
{{- if .Records}}
        <table>
          <thead>
            <tr>
              <th>id</th>
{{- range .Columns}}
              <th>{ {{js .Label}} }</th>
{{- end}}
            </tr>
          </thead>
          <tbody>
            {recent{{.Index}}.map((row) => (
              <tr key={String(row.id)}>
                <td>
{{- if .DetailRoute}}
                  <Link to={` + "`/{{.DetailRoute}}/${row.id}`" + `}>{String(row.id)}</Link>
{{- else}}
                  {String(row.id)}
{{- end}}
                </td>
{{- range .Columns}}
                <td>{String(row["{{.JSONName}}"] ?? "")}</td>
{{- end}}
              </tr>
            ))}
          </tbody>
        </table>
        {recent{{.Index}}.length === 0 ? <p>{ {{js (printf "No %s yet." .Label)}} }</p> : null}
{{- end}}
{{- if .ViewAllRoute}}
        <p>
          <Link to="/{{.ViewAllRoute}}">View all</Link>
        </p>
{{- end}}
      </section>
{{- end}}
    </section>
  );
}
`

// tablePageSrc renders a resource_table page: a paginated table populated from
// the collection GET, mirroring Gombit's own generated list page (openapi-fetch
// client, unwrap, the paths/schema types). It is page-driven — the component,
// title, columns and page size come from the spec's resource_table page — while
// its row and "New" links target the bound resource's canonical detail/form
// routes. Pagination is honored at runtime: the generated list handler already
// accepts page/per_page and returns a PageMeta total, so the table sends the
// configured page size and pages through the result. Search (TableConfig.Search),
// sortable column headers (Columns ∩ SortableFields) and exact-match filter
// controls (TableConfig.Filters) each wire the ?search= / ?ordering= /
// ?<field>= query params the generated list handler exposes (gombit #260).
const tablePageSrc = `{{.Banner}}
import { useEffect, useState } from "react";
import { Link } from "react-router";

import { useApiClient } from "../../api/client";
import { unwrap } from "../../api/generated/client";
import type { paths } from "../../api/generated/schema";

type ListResponse =
  paths["{{.CollectionPath}}"]["get"]["responses"][200]["content"]["application/json"];
type {{.Type}}Row = NonNullable<ListResponse["data"]>[number];

const PAGE_SIZE = {{.PageSize}};

export function {{.Component}}() {
  const client = useApiClient();
  const [rows, setRows] = useState<{{.Type}}Row[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
{{- if .Search}}
  const [search, setSearch] = useState("");
{{- end}}
{{- if .Sortable}}
  const [ordering, setOrdering] = useState("");
{{- end}}
{{- if .Filters}}
  const [filters, setFilters] = useState<Record<string, string>>({});
{{- end}}
  const [status, setStatus] = useState({{js (printf "Loading %s…" .Title)}});

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const listed = await unwrap(
          await client.GET("{{.CollectionPath}}", { params: { query: { page, per_page: PAGE_SIZE{{if .Search}}, search: search || undefined{{end}}{{if .Sortable}}, ordering: ordering || undefined{{end}}{{range .Filters}}, "{{.JSONName}}": filters["{{.JSONName}}"] || undefined{{end}} } } }),
        );
        if (cancelled) {
          return;
        }
        const data = Array.isArray(listed.data) ? listed.data : [];
        setRows(data);
        setTotal(listed.meta?.total ?? data.length);
        setStatus(data.length === 0 ? {{js (printf "No %s yet." .Title)}} : "");
      } catch (err: unknown) {
        if (cancelled) {
          return;
        }
        setStatus(err instanceof Error ? err.message : "request failed");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client, page{{if .Search}}, search{{end}}{{if .Sortable}}, ordering{{end}}{{if .Filters}}, filters{{end}}]);

  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));
{{- if .Filters}}

  // Set one exact-match filter (empty clears it) and return to the first page.
  const setFilter = (col: string, value: string) => {
    setFilters((prev) => ({ ...prev, [col]: value }));
    setPage(1);
  };
{{- end}}
{{- if .Sortable}}

  // Cycle a sortable column: unsorted → ascending → descending → unsorted. The
  // ?ordering= value is the storage-name column, "-" prefixed for descending
  // (gombit #260); id is the server default when ordering is cleared.
  const toggleSort = (col: string) => {
    setOrdering((cur) => (cur === col ? "-" + col : cur === "-" + col ? "" : col));
    setPage(1);
  };
{{- end}}

  return (
    <section>
      <h1>{ {{js .Title}} }</h1>
{{- if and .Create .FormRoute}}
      <p>
        <Link to="/{{.FormRoute}}/new">New {{.Type}}</Link>
      </p>
{{- end}}
{{- if .Search}}
      <p>
        <input
          type="search"
          value={search}
          placeholder={{js (printf "Search %s…" .Title)}}
          aria-label={{js (printf "Search %s" .Title)}}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(1);
          }}
        />
      </p>
{{- end}}
{{- if .Filters}}
      <div>
{{- range .Filters}}
{{- if eq .Control "bool"}}
        <select
          value={filters["{{.JSONName}}"] ?? ""}
          aria-label={{js (printf "Filter by %s" .Label)}}
          onChange={(e) => setFilter("{{.JSONName}}", e.target.value)}
        >
          <option value="">{ {{js (printf "%s: any" .Label)}} }</option>
          <option value="true">{ {{js "Yes"}} }</option>
          <option value="false">{ {{js "No"}} }</option>
        </select>
{{- else if eq .Control "enum"}}
        <select
          value={filters["{{.JSONName}}"] ?? ""}
          aria-label={{js (printf "Filter by %s" .Label)}}
          onChange={(e) => setFilter("{{.JSONName}}", e.target.value)}
        >
          <option value="">{ {{js (printf "%s: any" .Label)}} }</option>
{{- range .Options}}
          <option value={{js .Value}}>{ {{if .Label}}{{js .Label}}{{else}}{{js .Value}}{{end}} }</option>
{{- end}}
        </select>
{{- else}}
        <input
          type="{{.Control}}"
          value={filters["{{.JSONName}}"] ?? ""}
          placeholder={{js .Label}}
          aria-label={{js (printf "Filter by %s" .Label)}}
          onChange={(e) => setFilter("{{.JSONName}}", e.target.value)}
        />
{{- end}}
{{- end}}
      </div>
{{- end}}
      <table>
        <thead>
          <tr>
            <th>id</th>
{{- range .Columns}}
{{- if .Sortable}}
            <th aria-sort={ordering === "{{.JSONName}}" ? "ascending" : ordering === "-{{.JSONName}}" ? "descending" : "none"}>
              <button type="button" onClick={() => toggleSort("{{.JSONName}}")}>
                { {{js .Label}} }
                {ordering === "{{.JSONName}}" ? " ▲" : ordering === "-{{.JSONName}}" ? " ▼" : ""}
              </button>
            </th>
{{- else}}
            <th>{ {{js .Label}} }</th>
{{- end}}
{{- end}}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={String(row.id)}>
              <td>
{{- if .DetailRoute}}
                <Link to={` + "`/{{.DetailRoute}}/${row.id}`" + `}>{String(row.id)}</Link>
{{- else}}
                {String(row.id)}
{{- end}}
              </td>
{{- range .Columns}}
              <td>{String(row["{{.JSONName}}"] ?? "")}</td>
{{- end}}
            </tr>
          ))}
        </tbody>
      </table>
      <nav aria-label="Pagination">
        <button type="button" disabled={page <= 1} onClick={() => setPage((p) => Math.max(1, p - 1))}>
          Previous
        </button>
        <span>
          {"Page "}
          {page}
          {" of "}
          {pageCount}
        </span>
        <button type="button" disabled={page >= pageCount} onClick={() => setPage((p) => p + 1)}>
          Next
        </button>
      </nav>
      {status ? <p>{status}</p> : null}
    </section>
  );
}
`

// detailPageSrc renders a single record fetched by id (page-driven, #53). It
// shows the record's own fields and, per has_many relationship, a section that
// embeds the related records — the related collection filtered by the
// back-reference foreign key (?<fk>=<id>, a belongs_to default the generated
// list handler serves, gombit #260) — as a small table, with a "View all" link
// to the related table page for the rest. Its "back to list" link targets the
// resource's first table page and is omitted when it has none.
const detailPageSrc = `{{.Banner}}
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";

import { useApiClient } from "../../api/client";
import { unwrap } from "../../api/generated/client";
import type { paths } from "../../api/generated/schema";

type GetResponse =
  paths["{{.CollectionPath}}/{id}"]["get"]["responses"][200]["content"]["application/json"];
type {{.Type}}Record = NonNullable<GetResponse["data"]>;
{{- range .Related}}
type Related{{.Index}}Row = NonNullable<
  paths["{{.CollectionPath}}"]["get"]["responses"][200]["content"]["application/json"]["data"]
>[number];
{{- end}}
{{if .Related}}
// Embedded related lists show the first RELATED_PAGE_SIZE records back-referencing
// this record; the section links to the full table for the rest.
const RELATED_PAGE_SIZE = 10;
{{end}}
export function {{.Component}}() {
  const client = useApiClient();
  const { id = "" } = useParams();
  const [record, setRecord] = useState<{{.Type}}Record | null>(null);
  const [status, setStatus] = useState("Loading…");
{{- range .Related}}
  const [related{{.Index}}, setRelated{{.Index}}] = useState<Related{{.Index}}Row[]>([]);
{{- end}}

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const got = await unwrap(
          await client.GET("{{.CollectionPath}}/{id}", { params: { path: { id } } }),
        );
        if (cancelled) {
          return;
        }
        setRecord(got.data ?? null);
        setStatus("");
      } catch (err: unknown) {
        if (cancelled) {
          return;
        }
        setStatus(err instanceof Error ? err.message : "request failed");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client, id]);
{{- range .Related}}

  useEffect(() => {
    if (!id) {
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const listed = await unwrap(
          await client.GET("{{.CollectionPath}}", { params: { query: { "{{.FKParam}}": id, per_page: RELATED_PAGE_SIZE } } }),
        );
        if (!cancelled) {
          setRelated{{.Index}}(Array.isArray(listed.data) ? listed.data : []);
        }
      } catch {
        // A failed related fetch leaves the section empty; the record still shows.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client, id]);
{{- end}}

  return (
    <section>
      <h1>{{.Type}}</h1>
{{- if or .ListRoute (and .Update .FormRoute)}}
      <p>
{{- if .ListRoute}}
        <Link to="/{{.ListRoute}}">Back to { {{js .Title}} }</Link>
{{- end}}
{{- if and .ListRoute .Update .FormRoute}}
        {" · "}
{{- end}}
{{- if and .Update .FormRoute}}
        <Link to={` + "`/{{.FormRoute}}/${id}/edit`" + `}>Edit</Link>
{{- end}}
      </p>
{{- end}}
      {record ? (
        <dl>
          <dt>id</dt>
          <dd>{String(record.id)}</dd>
{{- range .Fields}}
          <dt>{ {{js .Label}} }</dt>
          <dd>{String(record["{{.JSONName}}"] ?? "")}</dd>
{{- end}}
        </dl>
      ) : null}
{{- if .Related}}
      <section aria-label="Related records">
{{- range .Related}}
        <div aria-label={ {{js .Label}} }>
          <h2>{ {{js .Label}} }</h2>
          <table>
            <thead>
              <tr>
                <th>id</th>
{{- range .Columns}}
                <th>{ {{js .Label}} }</th>
{{- end}}
              </tr>
            </thead>
            <tbody>
              {related{{.Index}}.map((row) => (
                <tr key={String(row.id)}>
                  <td>
{{- if .DetailRoute}}
                    <Link to={` + "`/{{.DetailRoute}}/${row.id}`" + `}>{String(row.id)}</Link>
{{- else}}
                    {String(row.id)}
{{- end}}
                  </td>
{{- range .Columns}}
                  <td>{String(row["{{.JSONName}}"] ?? "")}</td>
{{- end}}
                </tr>
              ))}
            </tbody>
          </table>
          {related{{.Index}}.length === 0 ? <p>{ {{js (printf "No %s yet." .Label)}} }</p> : null}
{{- if .ViewAllRoute}}
          <p>
            <Link to="/{{.ViewAllRoute}}">View all { {{js .Label}} }</Link>
          </p>
{{- end}}
        </div>
{{- end}}
      </section>
{{- end}}
      {status ? <p>{status}</p> : null}
    </section>
  );
}
`

// formPageSrc renders the write form. Its shape follows the resource's toggles:
// with both create and update it loads on an id and chooses POST or PUT; with
// only one, it is fixed to that operation, and the unused imports, load effect
// and branch are omitted so the module stays lint-clean. Empty date/datetime
// values with a wire type that rejects "" (time.Time, decimal.Decimal) are
// dropped from the request body, since the API rejects
// "" (only RFC 3339 or a missing/null key unmarshal). On success it navigates
// to the resource's first table page, or the app root when it has none.
const formPageSrc = `{{.Banner}}
import { useState{{if .NeedsEffect}}, useEffect{{end}} } from "react";
import { useForm } from "react-hook-form";
import { Link, useNavigate{{if .Update}}, useParams{{end}} } from "react-router";

import { useApiClient } from "../../api/client";
import { applyContractErrors } from "../../api/formErrors";
import { unwrap } from "../../api/generated/client";

type {{.Type}}FormValues = {
{{- range .Fields}}
  {{.JSONName}}: {{.TSType}};
{{- end}}
};

const emptyValues: {{.Type}}FormValues = {
{{- range .Fields}}
  {{.JSONName}}: {{if eq .TSType "number"}}0{{else if eq .TSType "boolean"}}false{{else}}""{{end}},
{{- end}}
};

export function {{.Component}}() {
  const client = useApiClient();
  const navigate = useNavigate();
{{- if .Update}}
  const { id = "" } = useParams();
{{- end}}
{{- if and .Create .Update}}
  const editing = id !== "";
{{- else if .Update}}
  const editing = true;
{{- else}}
  const editing = false;
{{- end}}
  const [status, setStatus] = useState("");
  const {
    register,
    handleSubmit,
{{- if .Update}}
    reset,
{{- end}}
    setError,
    formState: { errors, isSubmitting },
  } = useForm<{{.Type}}FormValues>({ defaultValues: emptyValues });
{{- range .RelationshipFields}}
  const [{{.OptionsVar}}, set{{.OptionsVar}}] = useState<{ id: string; label: string }[]>([]);
{{- end}}
{{- if .RelationshipFields}}

  // Populate the relationship selectors from each target resource's list.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
{{- range .RelationshipFields}}
      try {
        const listed = await unwrap(await client.GET("{{.TargetPath}}", { params: { query: { per_page: 100 } } }));
        if (!cancelled) {
          const rows = Array.isArray(listed.data) ? listed.data : [];
          set{{.OptionsVar}}(rows.map((r: Record<string, unknown>) => ({ id: String(r.id), label: String(r["{{.DisplayField}}"] ?? r.id) })));
        }
      } catch {
        // A failed options load leaves the selector empty rather than breaking the form.
      }
{{- end}}
    })();
    return () => {
      cancelled = true;
    };
  }, [client]);
{{- end}}
{{- if .Update}}

  useEffect(() => {
    if (!editing) {
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const got = await unwrap(
          await client.GET("{{.CollectionPath}}/{id}", { params: { path: { id } } }),
        );
        if (cancelled || !got.data) {
          return;
        }
        const record = got.data;
        reset({
{{- range .Fields}}
          {{.JSONName}}: (record["{{.JSONName}}"] ?? emptyValues.{{.JSONName}}) as {{.TSType}},
{{- end}}
        });
      } catch (err: unknown) {
        if (!cancelled) {
          setStatus(err instanceof Error ? err.message : "request failed");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client, editing, id, reset]);
{{- end}}

  async function onSubmit(values: {{.Type}}FormValues) {
    setStatus("");
    const body = { ...values };
{{- range .Fields}}
{{- if .OmitEmpty}}
    if (body.{{.JSONName}} === "") {
      delete (body as Partial<{{$.Type}}FormValues>).{{.JSONName}};
    }
{{- end}}
{{- end}}
    try {
{{- if and .Create .Update}}
      if (editing) {
        await unwrap(
          await client.PUT("{{.CollectionPath}}/{id}", { params: { path: { id } }, body }),
        );
      } else {
        await unwrap(await client.POST("{{.CollectionPath}}", { body }));
      }
{{- else if .Update}}
      await unwrap(
        await client.PUT("{{.CollectionPath}}/{id}", { params: { path: { id } }, body }),
      );
{{- else}}
      await unwrap(await client.POST("{{.CollectionPath}}", { body }));
{{- end}}
      navigate("{{if .ListRoute}}/{{.ListRoute}}{{else}}/{{end}}");
    } catch (err: unknown) {
      if (!applyContractErrors(setError, err)) {
        setStatus(err instanceof Error ? err.message : "request failed");
      }
    }
  }

  return (
    <section>
      <h1>{editing ? "Edit {{.Type}}" : "New {{.Type}}"}</h1>
{{- if .ListRoute}}
      <p>
        <Link to="/{{.ListRoute}}">Back to { {{js .Title}} }</Link>
      </p>
{{- end}}
      <form onSubmit={handleSubmit(onSubmit)}>
        <div className="form-fields form-layout-{{.Layout}}">
{{- range .Fields}}
        <label>
          { {{js .Label}} }
{{- if eq .Input "checkbox"}}
          <input type="checkbox" {...register("{{.JSONName}}")} />
{{- else if eq .Input "number"}}
          <input type="number" {...register("{{.JSONName}}", { setValueAs: (value) => (value === "" ? 0 : Number(value)){{if .Required}}, required: {{js (printf "%s is required" .Label)}}{{end}} })} />
{{- else if eq .Input "relationship"}}
          <select {...register("{{.JSONName}}", { setValueAs: (value) => (value === "" ? 0 : Number(value)){{if .Required}}, required: {{js (printf "%s is required" .Label)}}{{end}} })}>
            <option value="">—</option>
            {{"{"}}{{.OptionsVar}}.map((o) => (
              <option key={o.id} value={o.id}>{o.label}</option>
            )){{"}"}}
          </select>
{{- else if eq .Input "select"}}
          <select {...register("{{.JSONName}}"{{if .Required}}, { required: {{js (printf "%s is required" .Label)}} }{{end}})}>
            <option value="">—</option>
{{- range .Options}}
            <option value={{js .Value}}>{ {{if .Label}}{{js .Label}}{{else}}{{js .Value}}{{end}} }</option>
{{- end}}
          </select>
{{- else}}
          <input type="text"{{if .Placeholder}} placeholder="{{.Placeholder}}"{{end}} {...register("{{.JSONName}}"{{if .Required}}, { required: {{js (printf "%s is required" .Label)}} }{{end}})} />
{{- end}}
        </label>
        {errors.{{.JSONName}}?.message ? <p>{errors.{{.JSONName}}.message}</p> : null}
{{- end}}
        </div>
        <button type="submit" disabled={isSubmitting}>
          {editing ? "Save" : "Create"}
        </button>
      </form>
      {status ? <p>{status}</p> : null}
    </section>
  );
}
`

// registrySrc renders resources.tsx: the routes and metadata the application
// shell consumes. Table routes are page-driven (one per resource_table page);
// detail/form routes are resource-driven. Routes are RouteObjects so the
// scaffold router can spread them in; only the operations a resource enables are
// registered, and only resources with a table page appear in generatedResources
// (the nav metadata).
const registrySrc = `{{.Banner}}
import type { RouteObject } from "react-router";

{{range .Details -}}
import { {{.Component}} } from "./{{.Package}}/{{.Component}}";
{{end -}}
{{range .Forms -}}
import { {{.Component}} } from "./{{.Package}}/{{.Component}}";
{{end -}}
{{range .Tables -}}
import { {{.Component}} } from "./{{.Package}}/{{.Component}}";
{{end -}}
{{range .Dashboards -}}
import { {{.Component}} } from "./dashboard/{{.Component}}";
{{end}}
export type GeneratedResource = {
  slug: string;
  title: string;
  listPath: string;
};

export const generatedResources: GeneratedResource[] = [
{{- range .Dashboards}}
  { slug: "{{.Slug}}", title: {{js .Title}}, listPath: "/{{.Slug}}" },
{{- end}}
{{- range .Tables}}
  { slug: "{{.Slug}}", title: {{js .Title}}, listPath: "/{{.Slug}}" },
{{- end}}
];

// NavEntry is one authored navigation entry (DESIGN.md §4.5): an internal page
// route or an external URL, in authored order. The application shell renders
// these as the primary navigation; an entry is external iff its external flag
// is true.
export type NavEntry = {
  label: string;
  to: string;
  external: boolean;
};

export const generatedNavigation: NavEntry[] = [
{{- range .Navigation}}
  { label: {{js .Label}}, to: {{js .To}}, external: {{.External}} },
{{- end}}
];

// Branding is the generated application branding (DESIGN.md §19). The app shell
// applies the accent color, shows the name/logo and honors the appearance mode.
export type Branding = {
  appName: string;
  logoRef: string;
  accentColor: string;
  appearance: "light" | "dark" | "system";
};

export const generatedBranding: Branding = {
  appName: {{js .Branding.AppName}},
  logoRef: {{js .Branding.LogoRef}},
  accentColor: {{js .Branding.AccentColor}},
  appearance: {{js .Branding.Appearance}},
};

export const generatedResourceRoutes: RouteObject[] = [
{{- range .Dashboards}}
  { path: "{{.Slug}}", element: <{{.Component}} /> },
{{- end}}
{{- range .Tables}}
  { path: "{{.Slug}}", element: <{{.Component}} /> },
{{- end}}
{{- range .Forms}}
{{- if .Create}}
  { path: "{{.Slug}}/new", element: <{{.Component}} /> },
{{- end}}
{{- if .Update}}
  { path: "{{.Slug}}/:id/edit", element: <{{.Component}} /> },
{{- end}}
{{- end}}
{{- range .Details}}
  { path: "{{.Slug}}/:id", element: <{{.Component}} /> },
{{- end}}
];
`
