import {
  AuditTable,
  Empty,
  ErrorBox,
  Heading,
  Loading,
  Pagination,
} from "./components";
import { useResource } from "./resource";
import type { AuditItem, Page } from "./types";
import { pageNumber } from "./utils";

export default function AuditPage({ query }: { query: URLSearchParams }) {
  const resource = useResource<Page<AuditItem>>(
    `/audit?page=${pageNumber(query)}`,
  );
  return (
    <>
      <Heading
        title="Audit history"
        description="Recorded event transitions, worker claims, results, lease recovery and workspace controls."
      />
      <section className="sheet" aria-labelledby="audit-heading">
        <div className="section-bar">
          <h2 id="audit-heading">Activity register</h2>
          <button
            className="button text-button"
            disabled={resource.loading}
            onClick={resource.refresh}
          >
            Refresh history
          </button>
        </div>
        {resource.loading ? (
          <Loading>Loading audit records...</Loading>
        ) : resource.error ? (
          <ErrorBox message={resource.error} onRetry={resource.refresh} />
        ) : resource.data && resource.data.items.length === 0 ? (
          <Empty title="No activity recorded yet">
            <p>
              Create a synthetic event or change a workspace control to begin
              recording activity.
            </p>
          </Empty>
        ) : (
          resource.data && (
            <>
              <AuditTable items={resource.data.items} />
              <Pagination
                page={resource.data}
                label="records"
                onChange={(page) => {
                  window.location.hash = `/audit?page=${page}`;
                }}
              />
            </>
          )
        )}
      </section>
    </>
  );
}
