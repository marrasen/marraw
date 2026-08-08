// One labelled row of the settings dialog: a title, its explanation, and the
// control on the right. Shared by every section, including the remote one that
// now lives in its own file — hence a module of its own rather than an export
// from the dialog, which would have made the two import each other.
import * as React from 'react';

export function SettingRow({
  title,
  description,
  control,
}: {
  title: React.ReactNode;
  description: React.ReactNode;
  control?: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-4 border-b py-4 first:pt-0 last:border-0">
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{title}</div>
        <div className="mt-0.5 text-xs leading-normal text-muted-foreground">{description}</div>
      </div>
      {control && <div className="shrink-0">{control}</div>}
    </div>
  );
}
