import { RefreshCw, Check } from "lucide-react";
import type { PairingListResponse } from "@/types/instance";

interface PairingCardProps {
  data: PairingListResponse | undefined;
  isLoading: boolean;
  isError: boolean;
  onRefresh: () => void;
  onApprove: (code: string) => void;
  isApproving: boolean;
  approvedCode: string | null;
}

export default function PairingCard({
  data,
  isLoading,
  isError,
  onRefresh,
  onApprove,
  isApproving,
  approvedCode,
}: PairingCardProps) {

  if (isLoading && !data) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-medium text-gray-900">Feishu Pairing</h3>
        </div>
        <div className="text-sm text-gray-500">Loading pairing requests...</div>
      </div>
    );
  }

  if (isError && !data) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-medium text-gray-900">Feishu Pairing</h3>
          <button
            onClick={onRefresh}
            className="p-1 text-gray-400 hover:text-gray-600 rounded"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
        </div>
        <div className="text-sm text-red-600">Failed to load pairing requests.</div>
      </div>
    );
  }

  const pendingRequests = data?.pending ?? [];

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-sm font-medium text-gray-900">Feishu Pairing</h3>
          <p className="text-xs text-gray-500 mt-1">
            Approve Feishu DM access requests for this agent.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={onRefresh}
            className="p-1 text-gray-400 hover:text-gray-600 rounded"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
        </div>
      </div>

      {pendingRequests.length === 0 ? (
        <div className="text-sm text-gray-400 italic py-4">
          No pending pairing requests.
        </div>
      ) : (
        <div className="space-y-3">
          {pendingRequests.map((request) => {
            const isApproved = approvedCode === request.code;
            return (
              <div
                key={request.code}
                className="flex items-center justify-between p-3 bg-gray-50 rounded-md border border-gray-200"
              >
                <div className="flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-gray-900">
                      {request.code}
                    </span>
                    {isApproved && (
                      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-green-50 text-green-700">
                        <Check size={12} />
                        Approved
                      </span>
                    )}
                  </div>
                </div>
                <button
                  onClick={() => onApprove(request.code)}
                  disabled={isApproving || isApproved}
                  className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {isApproving ? "Approving..." : "Approve"}
                </button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
