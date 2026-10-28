import { useRef, type Dispatch, type SetStateAction } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { APIError, apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import { buildNodeRegistrationRequest } from "@/lib/node-registration";
import type { NodeRegistrationResponse, WorkerNode } from "@/types/domain";
import { type NodeConfigurationResponse, type NodeEditForm, nodeIdentity } from "./node-registration-model";
import { nodeMetadataWriteOutcomeUnknown, revalidateNodeMetadataEdit } from "./node-metadata-edit";



export function useNodeRegistrationMutations({
  registrationDraft,
  invalidateNodeQueries,
  setConfiguration,
  setCreateOpen,
  setEditingNode,
}: {
  registrationDraft: Parameters<typeof buildNodeRegistrationRequest>[0];
  invalidateNodeQueries: () => Promise<void>;
  setConfiguration: Dispatch<SetStateAction<NodeConfigurationResponse | null>>;
  setCreateOpen: (open: boolean) => void;
  setEditingNode: (node: WorkerNode | null) => void
}) {
  const queryClient = useQueryClient();
  const unresolvedEdits = useRef(new Set<string>());
  const pendingEdits = useRef(new Set<string>());

  const createToken = useMutation({
    mutationFn: () =>
      apiPost<NodeRegistrationResponse>("/nodes/registration-tokens", buildNodeRegistrationRequest(registrationDraft)),
    onSuccess: async (data) => {
      setConfiguration(data);
      setCreateOpen(false);
      await invalidateNodeQueries();
    },
  });
  const loadConfiguration = useMutation({
    mutationFn: (nodeID: string) => apiGet<NodeConfigurationResponse>(`/nodes/${encodeURIComponent(nodeID)}/configuration`),
    onSuccess: (data) => setConfiguration(data),
  });
  const regenerateConfigureToken = useMutation({
    mutationFn: (nodeID: string) => apiPost<NodeConfigurationResponse>(`/nodes/${encodeURIComponent(nodeID)}/configure-token`),
    onSuccess: async (data) => {
      setConfiguration(data);
      await invalidateNodeQueries();
    },
  });
  const rotateRuntimeToken = useMutation({
    mutationFn: (nodeID: string) => apiPost<NodeConfigurationResponse>(`/nodes/${encodeURIComponent(nodeID)}/rotate-token`),
    onSuccess: async (data) => {
      setConfiguration(data);
      await invalidateNodeQueries();
    },
  });
  const updateNode = useMutation({
    retry: false,
    mutationFn: async ({ nodeID, values, endpointless, baseline }: { nodeID: string; values: NodeEditForm; endpointless: boolean; baseline: WorkerNode }) => {
      if (unresolvedEdits.current.has(nodeID)) throw new APIError("Node edit outcome is unresolved", 409, "node_update_outcome_unknown");
      if (pendingEdits.current.has(nodeID)) throw new APIError("Node save is in progress", 409, "node_update_pending");
      pendingEdits.current.add(nodeID);
      try {
        const revision = await revalidateNodeMetadataEdit(queryClient, nodeID, baseline);
        try {
          return await apiPut<WorkerNode>(`/nodes/${encodeURIComponent(nodeID)}`, {
            service_name: values.service_name,
            description: values.description,
            expected_metadata_revision: revision,
            ...(endpointless
              ? {}
              : {
                  host: values.host,
                  port: Number.parseInt(values.port, 10),
                  ssl_enabled: values.ssl_enabled,
                }),
          });
        } catch (error) {
          if (nodeMetadataWriteOutcomeUnknown(error)) {
            unresolvedEdits.current.add(nodeID);
            throw new APIError("Node edit outcome is unresolved", 409, "node_update_outcome_unknown");
          }
          throw error;
        }
      } finally {
        pendingEdits.current.delete(nodeID);
      }
    },
    onSuccess: async (node) => {
      // A GET begun before the PUT must not roll the list back after the save.
      await queryClient.cancelQueries({ queryKey: ["nodes"] });
      queryClient.setQueryData<WorkerNode[]>(["nodes"], (rows) => rows?.map((row) => nodeIdentity(row) === nodeIdentity(node) ? node : row));
      setEditingNode(null);
      setConfiguration((current) => (current?.node && nodeIdentity(current.node) === nodeIdentity(node) ? { ...current, node } : current));
      await invalidateNodeQueries();
    },
  });
  const deleteNode = useMutation({
    mutationFn: (nodeID: string) => apiDelete<{ status: string }>(`/services/${encodeURIComponent(nodeID)}`),
    onSuccess: async (_data, nodeID) => {
      setConfiguration((current) => (current?.node && nodeIdentity(current.node) === nodeID ? null : current));
      await invalidateNodeQueries();
    },
  });
  return { createToken, loadConfiguration, regenerateConfigureToken, rotateRuntimeToken, updateNode, deleteNode };
}
