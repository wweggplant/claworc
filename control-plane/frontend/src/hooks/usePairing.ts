import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { successToast, errorToast } from "@/utils/toast";
import {
  listFeishuPairingRequests,
  approveFeishuPairingRequest,
  revokeFeishuPairing,
} from "@/api/instances";

export function useFeishuPairingRequests(
  instanceId: number,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["instances", instanceId, "pairing", "feishu"],
    queryFn: () => listFeishuPairingRequests(instanceId),
    enabled,
    refetchInterval: 30000, // 30s polling
  });
}

export function useApproveFeishuPairing(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (code: string) => approveFeishuPairingRequest(instanceId, code),
    onSuccess: () => {
      successToast("Pairing approved");
      qc.invalidateQueries({
        queryKey: ["instances", instanceId, "pairing", "feishu"],
      });
    },
    onError: (err) => {
      errorToast("Failed to approve pairing", err);
    },
  });
}

export function useRevokeFeishuPairing(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => revokeFeishuPairing(instanceId),
    onSuccess: () => {
      successToast("Feishu channel deleted");
      qc.invalidateQueries({
        queryKey: ["instances", instanceId, "pairing", "feishu"],
      });
    },
    onError: (err) => {
      errorToast("Failed to delete channel", err);
    },
  });
}
