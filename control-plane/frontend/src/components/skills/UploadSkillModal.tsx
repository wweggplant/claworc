import { useRef, useState } from "react";
import { AlertTriangle, Upload, X, FileText, Loader2 } from "lucide-react";
import { useUploadSkill } from "@/hooks/useSkills";

interface Props {
  onClose: () => void;
  onUploaded: () => void;
}

interface UploadStatus {
  file: File;
  status: "pending" | "uploading" | "success" | "error" | "conflict";
  error?: string;
}

export default function UploadSkillModal({ onClose, onUploaded }: Props) {
  const [dragging, setDragging] = useState(false);
  const [uploadQueue, setUploadQueue] = useState<UploadStatus[]>([]);
  const [conflictSlug, setConflictSlug] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const { mutate: upload } = useUploadSkill();

  const handleFiles = (files: FileList | null) => {
    if (!files) return;
    const newFiles: UploadStatus[] = [];
    for (let i = 0; i < files.length; i++) {
      const file = files[i];
      if (file && file.name.endsWith(".zip")) {
        newFiles.push({ file, status: "pending" });
      }
    }
    if (newFiles.length > 0) {
      setUploadQueue((prev) => [...prev, ...newFiles]);
      setConflictSlug(null);
    }
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(false);
    handleFiles(e.dataTransfer.files);
  };

  const doUpload = async (overwrite: boolean) => {
    // Reset error items to pending when retrying (e.g. after conflict → overwrite)
    if (overwrite) {
      setUploadQueue((prev) =>
        prev.map((s) => (s.status === "error" ? { ...s, status: "pending", error: undefined } : s))
      );
    }

    // Use a ref to track latest queue state through async callbacks
    let latestQueue = [...uploadQueue];
    const trackQueue = (updater: (prev: UploadStatus[]) => UploadStatus[]) => {
      setUploadQueue((prev) => {
        const next = updater(prev);
        latestQueue = next;
        return next;
      });
    };

    for (let i = 0; i < latestQueue.length; i++) {
      const item = latestQueue[i];
      if (!item || item.status !== "pending") continue;

      trackQueue((prev) =>
        prev.map((s, idx) =>
          idx === i ? { ...s, status: "uploading" } : s
        )
      );

      await new Promise<void>((resolve) => {
        upload(
          { file: item.file, overwrite },
          {
            onSuccess: () => {
              trackQueue((prev) =>
                prev.map((s, idx) =>
                  idx === i ? { ...s, status: "success" } : s
                )
              );
              resolve();
            },
            onError: (error) => {
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              if ((error as any)?.response?.status === 409 && !overwrite) {
                // eslint-disable-next-line @typescript-eslint/no-explicit-any
                const detail: string = (error as any)?.response?.data?.detail ?? "";
                const match = detail.match(/Skill '(.+)' already exists/);
                const slug = match?.[1] ?? item.file.name.replace(".zip", "");
                setConflictSlug(slug);
                trackQueue((prev) =>
                  prev.map((s, idx) =>
                    idx === i ? { ...s, status: "conflict" } : s
                  )
                );
              } else {
                trackQueue((prev) =>
                  prev.map((s, idx) =>
                    idx === i
                      ? { ...s, status: "error", error: "Upload failed" }
                      : s
                  )
                );
              }
              resolve();
            },
          },
        );
      });
    }

    // After all uploads complete, close modal if no conflict
    const hasConflict = latestQueue.some((s) => s.status === "conflict");
    const hasDone = latestQueue.some((s) => s.status === "success" || s.status === "error");
    if (!hasConflict && hasDone) {
      onUploaded();
      onClose();
    }
  };

  const handleSubmit = () => doUpload(false);

  const isUploading = uploadQueue.some((s) => s.status === "uploading");

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      if (conflictSlug) {
        setConflictSlug(null);
      } else {
        onClose();
      }
    }
    if (e.key === "Enter" && uploadQueue.length > 0 && !isUploading && !conflictSlug) {
      handleSubmit();
    }
  };

  const removeFile = (index: number) => {
    setUploadQueue((prev) => prev.filter((_, i) => i !== index));
  };

  const clearCompleted = () => {
    setUploadQueue((prev) => prev.filter((s) => s.status === "pending"));
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onKeyDown={handleKeyDown}
      tabIndex={-1}
    >
      <div className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4">
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">Upload Skills</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X size={18} />
          </button>
        </div>

        <div className="px-6 py-6 flex flex-col gap-4">
          {conflictSlug ? (
            <div className="rounded-lg border border-amber-200 bg-amber-50 p-4 flex gap-3">
              <AlertTriangle size={18} className="text-amber-500 mt-0.5 shrink-0" />
              <p className="text-sm text-amber-800">
                A skill named <strong>{conflictSlug}</strong> already exists. Overwrite it?
              </p>
            </div>
          ) : (
            <>
              {uploadQueue.length > 0 && (
                <div className="max-h-48 overflow-y-auto">
                  <div className="flex items-center justify-between mb-2">
                    <span className="text-xs font-medium text-gray-500">
                      {uploadQueue.length} file{uploadQueue.length > 1 ? "s" : ""} selected
                    </span>
                    {uploadQueue.some((s) => s.status !== "pending") && (
                      <button
                        onClick={clearCompleted}
                        className="text-xs text-blue-600 hover:text-blue-700"
                      >
                        Clear completed
                      </button>
                    )}
                  </div>
                  <div className="flex flex-col gap-2">
                    {uploadQueue.map((item, index) => (
                      <div
                        key={index}
                        className={`flex items-center gap-2 p-2 rounded-lg border ${
                          item.status === "success"
                            ? "border-green-200 bg-green-50"
                            : item.status === "error"
                            ? "border-red-200 bg-red-50"
                            : item.status === "conflict"
                            ? "border-amber-200 bg-amber-50"
                            : "border-gray-200 bg-white"
                        }`}
                      >
                        <FileText size={16} className="text-gray-400 shrink-0" />
                        <span className="flex-1 text-sm truncate">{item.file.name}</span>
                        {item.status === "pending" && (
                          <button
                            onClick={() => removeFile(index)}
                            className="text-gray-400 hover:text-gray-600"
                          >
                            <X size={14} />
                          </button>
                        )}
                        {item.status === "uploading" && (
                          <Loader2 size={14} className="animate-spin text-blue-500" />
                        )}
                        {item.status === "success" && (
                          <span className="text-xs text-green-600">✓</span>
                        )}
                        {item.status === "error" && (
                          <span className="text-xs text-red-600">✗</span>
                        )}
                        {item.status === "conflict" && (
                          <AlertTriangle size={14} className="text-amber-500" />
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}
              <div
                onDragOver={(e) => { e.preventDefault(); setDragging(true); }}
                onDragLeave={() => setDragging(false)}
                onDrop={handleDrop}
                onClick={() => inputRef.current?.click()}
                className={`border-2 border-dashed rounded-lg p-8 text-center cursor-pointer transition-colors ${
                  dragging
                    ? "border-blue-400 bg-blue-50"
                    : "border-gray-300 hover:border-gray-400"
                }`}
              >
                <Upload size={24} className="mx-auto mb-3 text-gray-400" />
                <p className="text-sm font-medium text-gray-700">
                  Drop .zip files here or click to browse
                </p>
                <p className="text-xs text-gray-400 mt-1">
                  Multiple files supported. Each zip must contain a SKILL.md with valid frontmatter.
                </p>
                <input
                  ref={inputRef}
                  type="file"
                  accept=".zip"
                  multiple
                  className="hidden"
                  onChange={(e) => {
                    handleFiles(e.target.files);
                  }}
                />
              </div>
            </>
          )}
        </div>

        <div className="px-6 pb-5 flex items-center justify-end gap-3">
          {conflictSlug ? (
            <>
              <button
                onClick={() => {
                  setConflictSlug(null);
                  // Reset conflict/error items back to pending
                  setUploadQueue((prev) =>
                    prev.map((s) =>
                      s.status === "error" || s.status === "conflict"
                        ? { ...s, status: "pending", error: undefined }
                        : s
                    )
                  );
                }}
                className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => doUpload(true)}
                disabled={isUploading}
                className="px-4 py-2 text-sm font-medium text-white bg-amber-500 rounded-lg hover:bg-amber-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {isUploading ? "Overwriting…" : "Overwrite"}
              </button>
            </>
          ) : (
            <>
              <button
                onClick={onClose}
                disabled={isUploading}
                className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                Cancel
              </button>
              <button
                onClick={handleSubmit}
                disabled={uploadQueue.length === 0 || isUploading}
                className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {isUploading
                  ? `Uploading…`
                  : uploadQueue.length === 0
                  ? "Upload"
                  : `Upload (${uploadQueue.length})`}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
