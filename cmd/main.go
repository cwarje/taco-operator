package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// Import your generated TacoOrder types. Adjust the module path as needed.
	tacoV1alpha1 "github.com/cwarje/taco-operator/pkg/apis/tacoorder/v1alpha1"
)

// scheme is a runtime.Scheme that holds all resource types we use.
var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	// Register TacoOrder CRD scheme.
	_ = tacoV1alpha1.AddToScheme(scheme)
}

// MealMe API constants
const (
	mealMeRestaurantSearchEndpoint = "https://api.mealme.ai/v1/restaurants/search"
	mealMeOrderEndpoint            = "https://api.mealme.ai/v1/orders"
	maxDeliveryDistance            = 5.0 // in miles
)

// TacoOrderReconciler watches for TacoOrder resources.
type TacoOrderReconciler struct {
	client.Client
}

// Reconcile fetches a TacoOrder object, processes it using MealMe's API, and updates its status.
func (r *TacoOrderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var order tacoV1alpha1.TacoOrder
	if err := r.Get(ctx, req.NamespacedName, &order); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ctrl.LoggerFrom(ctx).Info("Reconciling TacoOrder", "order", order.Name)

	// Execute the business logic for the TacoOrder.
	if err := ReconcileTacoOrder(ctx, r.Client, &order); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Failed to reconcile TacoOrder", "order", order.Name)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers this reconciler with the manager.
func (r *TacoOrderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tacoV1alpha1.TacoOrder{}).
		Complete(r)
}

// ReconcileTacoOrder contains the main business logic for processing a TacoOrder.
// It fetches Secrets, searches for a nearby taco restaurant, places the order via MealMe,
// and updates the order status.
func ReconcileTacoOrder(ctx context.Context, c client.Client, order *tacoV1alpha1.TacoOrder) error {
	// 1. Fetch payment and address Secrets.
	paymentSecret, err := getSecret(ctx, c, order.Namespace, order.Spec.PaymentSecretName)
	if err != nil {
		return fmt.Errorf("failed to fetch payment secret: %w", err)
	}
	addressSecret, err := getSecret(ctx, c, order.Namespace, order.Spec.AddressSecretName)
	if err != nil {
		return fmt.Errorf("failed to fetch address secret: %w", err)
	}

	// 2. Retrieve sensitive data.
	cardNumber := string(paymentSecret.Data["cardNumber"])
	cardExpiry := string(paymentSecret.Data["cardExpiry"])
	cardCvv := string(paymentSecret.Data["cardCvv"])

	street := string(addressSecret.Data["street"])
	city := string(addressSecret.Data["city"])
	state := string(addressSecret.Data["state"])
	zip := string(addressSecret.Data["zip"])
	fullAddress := fmt.Sprintf("%s, %s, %s %s", street, city, state, zip)

	// 3. Update order status to "Created".
	if err := updateOrderPhase(ctx, c, order, "Created"); err != nil {
		return err
	}

	// 4. Search for a nearby taco restaurant using MealMe API.
	restaurant, err := findNearestTacoRestaurant(fullAddress, order.Spec.Variety)
	if err != nil {
		updateOrderPhase(ctx, c, order, "Canceled")
		return fmt.Errorf("restaurant search failed: %w", err)
	}

	// 5. Place the taco order via MealMe API.
	orderID, err := placeTacoOrder(restaurant.ID, order.Spec.Quantity, order.Spec.Variety, fullAddress,
		cardNumber, cardExpiry, cardCvv)
	if err != nil {
		updateOrderPhase(ctx, c, order, "Canceled")
		return fmt.Errorf("order placement failed: %w", err)
	}

	// 6. Update status to "Paid" (order accepted and charged).
	if err := updateOrderPhase(ctx, c, order, "Paid"); err != nil {
		return err
	}

	// 7. (Optional) Mark as Delivered.
	if err := updateOrderPhase(ctx, c, order, "Delivered"); err != nil {
		return err
	}

	fmt.Printf("Successfully placed MealMe order [%s] for TacoOrder [%s]\n", orderID, order.Name)
	return nil
}

// Restaurant represents a taco restaurant from MealMe’s search response.
type Restaurant struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Distance float64 `json:"distance"` // in miles
}

// mealMeSearchResponse mirrors the MealMe API restaurant search response.
type mealMeSearchResponse struct {
	Restaurants []Restaurant `json:"restaurants"`
}

// findNearestTacoRestaurant calls MealMe’s restaurant search endpoint.
func findNearestTacoRestaurant(address string, variety string) (*Restaurant, error) {
	requestBody := map[string]interface{}{
		"query":        "tacos", // searching for taco restaurants
		"address":      address,
		"cuisine":      "Mexican",
		"max_distance": maxDeliveryDistance,
	}
	payloadBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", mealMeRestaurantSearchEndpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("MEALME_API_TOKEN")))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MealMe restaurant search failed, status code %d", resp.StatusCode)
	}

	var mmResp mealMeSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&mmResp); err != nil {
		return nil, err
	}

	// Return the first restaurant within acceptable distance.
	for _, r := range mmResp.Restaurants {
		if r.Distance <= maxDeliveryDistance {
			return &r, nil
		}
	}
	return nil, errors.New("no taco restaurants found within acceptable distance")
}

// mealMeOrderResponse mirrors the response from MealMe’s order endpoint.
type mealMeOrderResponse struct {
	OrderID string `json:"orderId"`
	Status  string `json:"status"`
}

// placeTacoOrder sends a request to MealMe’s order endpoint to place the taco order.
func placeTacoOrder(restaurantID string, quantity int, variety string, address string,
	cardNumber string, cardExpiry string, cardCvv string) (string, error) {

	requestBody := map[string]interface{}{
		"restaurantId": restaurantID,
		"items": []map[string]interface{}{
			{
				"name":     fmt.Sprintf("%s taco", variety),
				"quantity": quantity,
			},
		},
		"deliveryAddress": address,
		"payment": map[string]string{
			"cardNumber": cardNumber,
			"cardExpiry": cardExpiry,
			"cardCvv":    cardCvv,
		},
	}
	payloadBytes, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", mealMeOrderEndpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("MEALME_API_TOKEN")))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("MealMe order failed, status code %d", resp.StatusCode)
	}

	var mmResp mealMeOrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&mmResp); err != nil {
		return "", err
	}

	if mmResp.Status != "ACCEPTED" && mmResp.Status != "PENDING" {
		return "", fmt.Errorf("MealMe order not accepted; status=%s", mmResp.Status)
	}

	return mmResp.OrderID, nil
}

// getSecret retrieves a Secret resource by name.
func getSecret(ctx context.Context, c client.Client, namespace, secretName string) (*v1.Secret, error) {
	secret := &v1.Secret{}
	err := c.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, secret)
	return secret, err
}

// updateOrderPhase updates the TacoOrder status.phase field.
func updateOrderPhase(ctx context.Context, c client.Client, order *tacoV1alpha1.TacoOrder, phase string) error {
	order.Status.Phase = phase
	return c.Status().Update(ctx, order)
}

// main sets up the manager, registers the controller, and starts the manager.
func main() {
	// Set up logging.
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Create a new manager with the in-cluster config.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: ":8080",
		Port:               9443,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to start manager: %v\n", err)
		os.Exit(1)
	}

	// Register the TacoOrderReconciler with the manager.
	if err = (&TacoOrderReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "unable to create controller: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "problem running manager: %v\n", err)
		os.Exit(1)
	}
}
