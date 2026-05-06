import { Badge } from "@/components/ui/badge";

// GroupBadge renders a credential / token group label with built-in-aware
// styling. The reserved "new" group is always shown uppercase as "NEW" with
// an amber accent so operators can tell at a glance that a credential sits
// in the shared-but-time-limited tier (see internal/auth/schedule.go).
// Public / unset groups render slate; other custom groups render violet.
export function GroupBadge({ group, title }: { group?: string; title?: string }) {
  const g = (group || "").toLowerCase();
  if (g === "new") {
    return (
      <Badge
        variant="amber"
        className="font-semibold tracking-wider"
        title={title || "Built-in shared pool with scheduled downtime (10h/day)"}
      >
        NEW
      </Badge>
    );
  }
  return (
    <Badge variant={group ? "violet" : "slate"} title={title || "Credential group"}>
      {group || "public"}
    </Badge>
  );
}
