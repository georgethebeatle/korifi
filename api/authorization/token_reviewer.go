package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "code.cloudfoundry.org/korifi/api/errors"
	authv1 "k8s.io/api/authentication/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//+kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create

const (
	serviceAccountsGroup     = "system:serviceaccounts"
	serviceAccountNamePrefix = "system:serviceaccount:"
)

type TokenReviewer struct {
	privilegedClient client.Client
}

func NewTokenReviewer(privilegedClient client.Client) *TokenReviewer {
	return &TokenReviewer{privilegedClient: privilegedClient}
}

func (r *TokenReviewer) WhoAmI(ctx context.Context, token *Token) (Identity, error) {
	tokenReview := &authv1.TokenReview{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tokenReview",
		},
		Spec: authv1.TokenReviewSpec{
			Token: string((*token).get()),
		},
	}
	err := r.privilegedClient.Create(ctx, tokenReview)
	if err != nil {
		return Identity{}, fmt.Errorf("failed to create token review: %w", apierrors.FromK8sError(err, "TokenReview"))
	}

	if !tokenReview.Status.Authenticated {
		return Identity{}, apierrors.NewInvalidAuthError(errors.New("not authenticated"))
	}

	idKind := rbacv1.UserKind
	idName := tokenReview.Status.User.Username

	if isServiceAccount(tokenReview.Status.User) {
		idKind = rbacv1.ServiceAccountKind

		if !strings.HasPrefix(idName, serviceAccountNamePrefix) {
			return Identity{}, fmt.Errorf("invalid serviceaccount name: %q", idName)
		}
		nameSegments := strings.Split(idName, ":")
		idName = nameSegments[len(nameSegments)-1]
	}

	var guid string

	var x interface{} = *token

	switch x.(type) {
	case *JwtToken:
		guid = ((*token).(*JwtToken)).getClaim("user_id")
	}

	return Identity{
		Name: idName,
		GUID: guid,
		Kind: idKind,
	}, nil
}

func isServiceAccount(subject authv1.UserInfo) bool {
	return contains(subject.Groups, serviceAccountsGroup)
}

func contains(groups []string, soughtGroup string) bool {
	for _, group := range groups {
		if group == soughtGroup {
			return true
		}
	}
	return false
}
