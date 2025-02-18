package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TacoOrderSpec defines the desired state of TacoOrder.
type TacoOrderSpec struct {
	// Quantity is the number of tacos to order.
	// +kubebuilder:validation:Minimum=1
	Quantity int `json:"quantity"`

	// Variety is the type of taco to order (e.g., "carnitas", "al pastor").
	// +optional
	Variety string `json:"variety,omitempty"`

	// PaymentSecretName is the name of the Kubernetes Secret containing credit card data.
	PaymentSecretName string `json:"paymentSecretName"`

	// AddressSecretName is the name of the Kubernetes Secret containing delivery address data.
	AddressSecretName string `json:"addressSecretName"`
}

// TacoOrderStatus defines the observed state of TacoOrder.
type TacoOrderStatus struct {
	// Phase indicates the current state of the order (e.g., "Created", "Paid", "Delivered", "Canceled").
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TacoOrder is the Schema for the tacoorders API.
type TacoOrder struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TacoOrderSpec   `json:"spec,omitempty"`
	Status TacoOrderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TacoOrderList contains a list of TacoOrder.
type TacoOrderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TacoOrder `json:"items"`
}
