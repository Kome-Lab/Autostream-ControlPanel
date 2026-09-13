import type { Dispatch, SetStateAction } from "react";
import { useMutation } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import { buildNodeRegistrationRequest } from "@/lib/node-registration";
import type { NodeRegistrationResponse, WorkerNode } from "@/types/domain";
import { type NodeConfigurationResponse, type NodeEditForm, nodeIdentity } from "./node-registration-model";



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
    mutationFn: ({ nodeID, values, endpointless }: { nodeID: string; values: NodeEditForm; endpointless: boolean }) =>
      apiPut<WorkerNode>(`/nodes/${encodeURIComponent(nodeID)}`, {
        service_name: values.service_name,
        description: values.description,
        ...(endpointless
          ? {}
          : {
              host: values.host,
              port: Number.parseInt(values.port, 10),
              ssl_enabled: values.ssl_enabled,
            }),
      }),
    onSuccess: async (node) => {
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
